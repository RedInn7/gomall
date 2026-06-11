package search

import (
	"context"

	conf "github.com/RedInn7/gomall/config"
	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/internal/product"
	"github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/es"
	"github.com/RedInn7/gomall/types"
)

// ProductSearch ES 可用时走 ES 模糊搜索；不可用时退化到 DB SearchProduct。
func ProductSearch(ctx context.Context, req *product.ProductSearchReq) (resp interface{}, err error) {
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
		log.LogrusObj.Errorf("ES search failed, fall back to DB: %v", esErr)
	}

	products, count, err := product.NewProductDao(ctx).SearchProduct(req.Info, req.BasePage)
	if err != nil {
		log.LogrusObj.Error(err)
		return
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
