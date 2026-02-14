package v1

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/pkg/utils/ctl"
	"github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/service"
	"github.com/RedInn7/gomall/types"
)

// InitSkillProductHandler 初始化秒杀商品信息
func InitSkillProductHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var req types.ListSkillProductReq
		if err := ctx.ShouldBind(&req); err != nil {
			// 参数校验
			log.LogrusObj.Infoln(err)
			ctx.JSON(http.StatusOK, ErrorResponse(ctx, err))
			return
		}

		l := service.GetSkillProductSrv()
		resp, err := l.InitSkillGoods(ctx.Request.Context())
		if err != nil {
			log.LogrusObj.Infoln(err)
			ctx.JSON(http.StatusOK, ErrorResponse(ctx, err))
			return
		}
		ctx.JSON(http.StatusOK, ctl.RespSuccess(ctx, resp))
	}
}

// ListSkillProductHandler 初始化秒杀商品信息
func ListSkillProductHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var req types.ListSkillProductReq
		if err := ctx.ShouldBind(&req); err != nil {
			// 参数校验
			log.LogrusObj.Infoln(err)
			ctx.JSON(http.StatusOK, ErrorResponse(ctx, err))
			return
		}

		l := service.GetSkillProductSrv()
		resp, err := l.ListSkillGoods(ctx.Request.Context())
		if err != nil {
			log.LogrusObj.Infoln(err)
			ctx.JSON(http.StatusOK, ErrorResponse(ctx, err))
			return
		}
		ctx.JSON(http.StatusOK, ctl.RespSuccess(ctx, resp))
	}
}

// GetSkillProductHandler 获取秒杀商品的详情
func GetSkillProductHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var req types.GetSkillProductReq
		if err := ctx.ShouldBind(&req); err != nil {
			// 参数校验
			log.LogrusObj.Infoln(err)
			ctx.JSON(http.StatusOK, ErrorResponse(ctx, err))
			return
		}

		l := service.GetSkillProductSrv()
		resp, err := l.GetSkillGoods(ctx.Request.Context(), &req)
		if err != nil {
			log.LogrusObj.Infoln(err)
			ctx.JSON(http.StatusOK, ErrorResponse(ctx, err))
			return
		}
		ctx.JSON(http.StatusOK, ctl.RespSuccess(ctx, resp))
	}
}

func SkillProductHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var req types.SkillProductReq
		if err := ctx.ShouldBind(&req); err != nil {
			// 参数校验
			log.LogrusObj.Infoln(err)
			ctx.JSON(http.StatusOK, ErrorResponse(ctx, err))
			return
		}

		l := service.GetSkillProductSrv()
		resp, err := l.SkillProduct(ctx.Request.Context(), &req)
		if err != nil {
			log.LogrusObj.Infoln(err)
			ctx.JSON(http.StatusOK, ErrorResponse(ctx, err))
			return
		}
		ctx.JSON(http.StatusOK, ctl.RespSuccess(ctx, resp))
	}
}
