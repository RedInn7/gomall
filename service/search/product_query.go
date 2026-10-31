package search

import (
	"context"
	"errors"
	"fmt"

	conf "github.com/RedInn7/gomall/config"
	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/internal/product"
	"github.com/RedInn7/gomall/pkg/e"
	"github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/es"
	"github.com/RedInn7/gomall/types"
)

// maxResultWindow ES 默认 index.max_result_window：from+size 超过它 ES 直接 400，DB 回退则是深 OFFSET 扫表
const maxResultWindow = 10000

var errSearchTooDeep = &e.Error{Code: e.InvalidParams, Msg: fmt.Sprintf("搜索结果最多只能翻到前 %d 条", maxResultWindow)}

// dbSearch DB LIKE 回退，单测替换以断言是否降级、降级时用的关键词
var dbSearch = func(ctx context.Context, keyword string, page types.BasePage) ([]*product.Product, int64, error) {
	return product.NewProductDao(ctx).SearchProduct(keyword, page)
}

// ProductSearch ES 可用时走 ES 模糊搜索；ES 连接失败或 5xx 时退化到 DB SearchProduct。
func ProductSearch(ctx context.Context, req *product.ProductSearchReq) (resp *types.DataListResp, err error) {
	req.BasePage.Normalize()
	// 写成除法避免超大 page_num 相乘溢出
	if req.PageNum > maxResultWindow/req.PageSize {
		return nil, errSearchTooDeep
	}
	// ES 与 DB 回退必须用同一个关键词；关键词为空时两边都不该查（ES 返回空，DB 会变成 LIKE '%%'）
	kw := searchKeyword(req)
	if kw == "" {
		return &types.DataListResp{Item: []*product.ProductResp{}, Total: 0}, nil
	}
	if es.EsClient != nil {
		docs, total, esErr := SearchProducts(ctx, req)
		if esErr == nil {
			pRespList := make([]*product.ProductResp, 0, len(docs))
			for _, d := range docs {
				pResp := &product.ProductResp{
					ID:            d.ID,
					Name:          d.Name,
					CategoryID:    d.CategoryID,
					Title:         d.Title,
					Info:          d.Info,
					ImgPath:       d.ImgPath,
					Price:         d.Price,
					DiscountPrice: d.DiscountPrice,
					CreatedAt:     d.CreatedAt,
					Num:           d.Num,
					OnSale:        d.OnSale,
					BossID:        d.BossID,
				}
				if conf.Config.System.UploadModel == consts.UploadModelLocal {
					pResp.ImgPath = conf.Config.PhotoPath.PhotoHost + conf.Config.System.HttpPort + conf.Config.PhotoPath.ProductPath + pResp.ImgPath
				}
				pRespList = append(pRespList, pResp)
			}
			return &types.DataListResp{Item: pRespList, Total: total}, nil
		}
		// 4xx 是请求本身的问题，换 DB 重查只会把一个错误请求变成慢查询
		if errors.Is(esErr, es.ErrBadRequest) {
			return nil, esErr
		}
		log.LogrusObj.Errorf("ES search failed, fall back to DB: %v", esErr)
	}

	products, count, err := dbSearch(ctx, kw, req.BasePage)
	if err != nil {
		log.LogrusObj.Error(err)
		return nil, err
	}

	pRespList := make([]*product.ProductResp, 0)
	for _, p := range products {
		pResp := &product.ProductResp{
			ID:            p.ID,
			Name:          p.Name,
			CategoryID:    p.CategoryID,
			Title:         p.Title,
			Info:          p.Info,
			ImgPath:       p.ImgPath,
			Price:         p.Price,
			DiscountPrice: p.DiscountPrice,
			View:          p.View(),
			CreatedAt:     p.CreatedAt.Unix(),
			Num:           p.Num,
			OnSale:        p.OnSale,
			BossID:        p.BossID,
			BossName:      p.BossName,
			BossAvatar:    p.BossAvatar,
		}
		if conf.Config.System.UploadModel == consts.UploadModelLocal {
			pResp.BossAvatar = conf.Config.PhotoPath.PhotoHost + conf.Config.System.HttpPort + conf.Config.PhotoPath.AvatarPath + pResp.BossAvatar
			pResp.ImgPath = conf.Config.PhotoPath.PhotoHost + conf.Config.System.HttpPort + conf.Config.PhotoPath.ProductPath + pResp.ImgPath
		}
		pRespList = append(pRespList, pResp)
	}

	resp = &types.DataListResp{
		Item:  pRespList,
		Total: count,
	}

	return
}
