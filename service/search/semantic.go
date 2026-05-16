package search

import (
	"context"
	"errors"
	"sort"

	conf "github.com/RedInn7/gomall/config"
	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/repository/db/dao"
	"github.com/RedInn7/gomall/repository/db/model"
	"github.com/RedInn7/gomall/repository/es"
	"github.com/RedInn7/gomall/types"
)

// hybrid 权重 50/50，作为业务调优入口；如需调整可改成读配置
const (
	defaultTopK = 10
	maxTopK     = 50

	weightSemantic = 0.5
	weightKeyword  = 0.5
)

// embedFunc 抽象 EmbedText，便于单测注入
type embedFunc func(ctx context.Context, text string) ([]float32, error)

// keywordSearchFunc 抽象 ES 关键词检索，便于单测注入
type keywordSearchFunc func(ctx context.Context, keyword string, from, size int, categoryID *uint) ([]es.ScoredProductDoc, int64, error)

// productLoaderFunc 抽象 DB 批量取商品，便于单测注入
type productLoaderFunc func(ctx context.Context, ids []uint) ([]*model.Product, error)

// hybridDeps 把可注入的依赖封装起来，生产用 defaultHybridDeps，单测可重写
type hybridDeps struct {
	embed   embedFunc
	keyword keywordSearchFunc
	loader  productLoaderFunc
}

func defaultHybridDeps() hybridDeps {
	return hybridDeps{
		embed:   EmbedText,
		keyword: es.SearchProductsWithScore,
		loader:  loadProductsByIDs,
	}
}

func loadProductsByIDs(ctx context.Context, ids []uint) ([]*model.Product, error) {
	return dao.NewProductDao(ctx).ListByIDs(ids)
}

// SemanticSearch 走 embedding + Milvus 召回，再与 ES 关键词召回做加权融合后截断 TopK
func SemanticSearch(ctx context.Context, req *types.ProductSemanticSearchReq) ([]types.ProductSemanticHit, error) {
	return semanticSearchWith(ctx, req, defaultHybridDeps())
}

func semanticSearchWith(ctx context.Context, req *types.ProductSemanticSearchReq, deps hybridDeps) ([]types.ProductSemanticHit, error) {
	if req == nil || req.Query == "" {
		return nil, errors.New("query 不能为空")
	}
	topK := req.TopK
	if topK <= 0 {
		topK = defaultTopK
	}
	if topK > maxTopK {
		topK = maxTopK
	}

	vec, err := deps.embed(ctx, req.Query)
	if err != nil {
		return nil, err
	}

	searcher := GetSearcher()
	// 向量召回取 topK*3，给融合留余量
	vecHits, err := searcher.Search(ctx, vec, topK*3, req.CategoryID)
	if err != nil {
		return nil, err
	}

	// 关键词召回同样取一个余量
	keywordHits, _, err := deps.keyword(ctx, req.Query, 0, topK*3, req.CategoryID)
	if err != nil {
		// ES 不可用不应该直接挂掉语义检索，记日志后降级走纯向量
		keywordHits = nil
	}

	semNorm := minMaxNormalize(vecScores(vecHits))
	kwNorm := minMaxNormalize(esScores(keywordHits))

	fused := make(map[uint]*types.ProductSemanticHit)
	for i, h := range vecHits {
		id := uint(h.ID)
		if id == 0 {
			continue
		}
		hit := getOrInit(fused, id)
		hit.SemanticScore = semNorm[i]
	}
	for i, h := range keywordHits {
		id := h.Doc.ID
		hit := getOrInit(fused, id)
		hit.KeywordScore = kwNorm[i]
	}

	ids := make([]uint, 0, len(fused))
	for id, h := range fused {
		h.Score = weightSemantic*h.SemanticScore + weightKeyword*h.KeywordScore
		ids = append(ids, id)
	}

	products, err := deps.loader(ctx, ids)
	if err != nil {
		return nil, err
	}
	prodMap := make(map[uint]*model.Product, len(products))
	for _, p := range products {
		prodMap[p.ID] = p
	}

	out := make([]types.ProductSemanticHit, 0, len(fused))
	for id, h := range fused {
		p, ok := prodMap[id]
		if !ok {
			continue
		}
		h.Product = productRespFromModel(p)
		out = append(out, *h)
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score == out[j].Score {
			return out[i].Product.ID < out[j].Product.ID
		}
		return out[i].Score > out[j].Score
	})

	if len(out) > topK {
		out = out[:topK]
	}
	return out, nil
}

func getOrInit(m map[uint]*types.ProductSemanticHit, id uint) *types.ProductSemanticHit {
	if h, ok := m[id]; ok {
		return h
	}
	h := &types.ProductSemanticHit{}
	m[id] = h
	return h
}

func vecScores(hits []Hit) []float32 {
	out := make([]float32, len(hits))
	for i, h := range hits {
		out[i] = h.Score
	}
	return out
}

func esScores(hits []es.ScoredProductDoc) []float32 {
	out := make([]float32, len(hits))
	for i, h := range hits {
		out[i] = h.Score
	}
	return out
}

// minMaxNormalize 把分数线性压到 [0,1]；同值或空集合返回与输入等长的 0 切片
func minMaxNormalize(scores []float32) []float32 {
	if len(scores) == 0 {
		return nil
	}
	out := make([]float32, len(scores))
	minV := scores[0]
	maxV := scores[0]
	for _, s := range scores[1:] {
		if s < minV {
			minV = s
		}
		if s > maxV {
			maxV = s
		}
	}
	if maxV == minV {
		// 全部命中等价，给个常量 1 表示都有效
		for i := range out {
			out[i] = 1
		}
		return out
	}
	span := maxV - minV
	for i, s := range scores {
		out[i] = (s - minV) / span
	}
	return out
}

func productRespFromModel(p *model.Product) *types.ProductResp {
	resp := &types.ProductResp{
		ID:            p.ID,
		Name:          p.Name,
		CategoryID:    p.CategoryID,
		Title:         p.Title,
		Info:          p.Info,
		ImgPath:       p.ImgPath,
		Price:         p.Price,
		DiscountPrice: p.DiscountPrice,
		CreatedAt:     p.CreatedAt.Unix(),
		Num:           p.Num,
		OnSale:        p.OnSale,
		BossID:        p.BossID,
		BossName:      p.BossName,
		BossAvatar:    p.BossAvatar,
	}
	if conf.Config != nil && conf.Config.System != nil && conf.Config.PhotoPath != nil && conf.Config.System.UploadModel == consts.UploadModelLocal {
		resp.BossAvatar = conf.Config.PhotoPath.PhotoHost + conf.Config.System.HttpPort + conf.Config.PhotoPath.AvatarPath + resp.BossAvatar
		resp.ImgPath = conf.Config.PhotoPath.PhotoHost + conf.Config.System.HttpPort + conf.Config.PhotoPath.ProductPath + resp.ImgPath
	}
	return resp
}
