package skill

import (
	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/internal/shared/response"
)

// InitSkillProductHandler 初始化秒杀商品信息
func InitSkillProductHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		_, ok := response.Bind[ListSkillProductReq](ctx)
		if !ok {
			return
		}

		l := GetSkillProductSrv()
		resp, err := l.InitSkillGoods(ctx.Request.Context())
		if err != nil {
			response.Fail(ctx, err)
			return
		}
		response.OK(ctx, resp)
	}
}

// ListSkillProductHandler 秒杀商品列表
func ListSkillProductHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		_, ok := response.Bind[ListSkillProductReq](ctx)
		if !ok {
			return
		}

		l := GetSkillProductSrv()
		resp, err := l.ListSkillGoods(ctx.Request.Context())
		if err != nil {
			response.Fail(ctx, err)
			return
		}
		response.OK(ctx, resp)
	}
}

// GetSkillProductHandler 获取秒杀商品的详情
func GetSkillProductHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		req, ok := response.Bind[GetSkillProductReq](ctx)
		if !ok {
			return
		}

		l := GetSkillProductSrv()
		resp, err := l.GetSkillGoods(ctx.Request.Context(), req)
		if err != nil {
			response.Fail(ctx, err)
			return
		}
		response.OK(ctx, resp)
	}
}

// SkillProductHandler 秒杀下单
func SkillProductHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		req, ok := response.Bind[SkillProductReq](ctx)
		if !ok {
			return
		}

		l := GetSkillProductSrv()
		resp, err := l.SkillProduct(ctx.Request.Context(), req)
		if err != nil {
			response.Fail(ctx, err)
			return
		}
		response.OK(ctx, resp)
	}
}
