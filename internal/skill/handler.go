package skill

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/internal/shared/response"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
	"github.com/RedInn7/gomall/pkg/utils/log"
)

// InitSkillProductHandler 初始化秒杀商品信息
func InitSkillProductHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var req ListSkillProductReq
		if err := ctx.ShouldBind(&req); err != nil {
			log.LogrusObj.Infoln(err)
			ctx.JSON(http.StatusOK, response.ErrorResponse(ctx, err))
			return
		}

		l := GetSkillProductSrv()
		resp, err := l.InitSkillGoods(ctx.Request.Context())
		if err != nil {
			log.LogrusObj.Infoln(err)
			ctx.JSON(http.StatusOK, response.ErrorResponse(ctx, err))
			return
		}
		ctx.JSON(http.StatusOK, ctl.RespSuccess(ctx, resp))
	}
}

// ListSkillProductHandler 秒杀商品列表
func ListSkillProductHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var req ListSkillProductReq
		if err := ctx.ShouldBind(&req); err != nil {
			log.LogrusObj.Infoln(err)
			ctx.JSON(http.StatusOK, response.ErrorResponse(ctx, err))
			return
		}

		l := GetSkillProductSrv()
		resp, err := l.ListSkillGoods(ctx.Request.Context())
		if err != nil {
			log.LogrusObj.Infoln(err)
			ctx.JSON(http.StatusOK, response.ErrorResponse(ctx, err))
			return
		}
		ctx.JSON(http.StatusOK, ctl.RespSuccess(ctx, resp))
	}
}

// GetSkillProductHandler 获取秒杀商品的详情
func GetSkillProductHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var req GetSkillProductReq
		if err := ctx.ShouldBind(&req); err != nil {
			log.LogrusObj.Infoln(err)
			ctx.JSON(http.StatusOK, response.ErrorResponse(ctx, err))
			return
		}

		l := GetSkillProductSrv()
		resp, err := l.GetSkillGoods(ctx.Request.Context(), &req)
		if err != nil {
			log.LogrusObj.Infoln(err)
			ctx.JSON(http.StatusOK, response.ErrorResponse(ctx, err))
			return
		}
		ctx.JSON(http.StatusOK, ctl.RespSuccess(ctx, resp))
	}
}

// SkillProductHandler 秒杀下单
func SkillProductHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var req SkillProductReq
		if err := ctx.ShouldBind(&req); err != nil {
			log.LogrusObj.Infoln(err)
			ctx.JSON(http.StatusOK, response.ErrorResponse(ctx, err))
			return
		}

		l := GetSkillProductSrv()
		resp, err := l.SkillProduct(ctx.Request.Context(), &req)
		if err != nil {
			log.LogrusObj.Infoln(err)
			ctx.JSON(http.StatusOK, response.ErrorResponse(ctx, err))
			return
		}
		ctx.JSON(http.StatusOK, ctl.RespSuccess(ctx, resp))
	}
}
