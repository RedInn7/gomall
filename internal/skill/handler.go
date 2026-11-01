package skill

import (
	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/internal/shared/response"
)

// InitSkillProductHandler 创建秒杀活动并预热（幂等）
func InitSkillProductHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		req, ok := response.BindJSON[SkillProductInitReq](ctx)
		if !ok {
			return
		}

		l := GetSkillProductSrv()
		resp, err := l.InitSkillGoods(ctx.Request.Context(), req)
		if err != nil {
			response.Fail(ctx, err)
			return
		}
		response.OK(ctx, resp)
	}
}

// ListSkillProductHandler 秒杀活动列表（进行中 + 未开始）
func ListSkillProductHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		l := GetSkillProductSrv()
		resp, err := l.ListSkillGoods(ctx.Request.Context())
		if err != nil {
			response.Fail(ctx, err)
			return
		}
		response.OK(ctx, resp)
	}
}

// GetSkillProductHandler 秒杀活动详情
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

// SkillProductHandler 秒杀下单：返回 ticket，前端轮询 GET orders/status?ticket=... 拿订单号
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
