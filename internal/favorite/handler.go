package favorite

import (
	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/internal/shared/response"
)

// CreateFavoriteHandler 创建收藏
func CreateFavoriteHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		req, ok := response.Bind[FavoriteCreateReq](ctx)
		if !ok {
			return
		}

		l := GetFavoriteSrv()
		resp, err := l.FavoriteCreate(ctx.Request.Context(), req)
		if err != nil {
			response.Fail(ctx, err)
			return
		}
		response.OK(ctx, resp)
	}
}

// ListFavoritesHandler 收藏夹详情接口
func ListFavoritesHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		req, ok := response.Bind[FavoritesServiceReq](ctx)
		if !ok {
			return
		}
		if req.PageSize == 0 {
			req.PageSize = consts.BasePageSize
		}

		l := GetFavoriteSrv()
		resp, err := l.FavoriteList(ctx.Request.Context(), req)
		if err != nil {
			response.Fail(ctx, err)
			return
		}
		response.OK(ctx, resp)
	}
}

// DeleteFavoriteHandler 删除收藏夹
func DeleteFavoriteHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		req, ok := response.Bind[FavoriteDeleteReq](ctx)
		if !ok {
			return
		}

		l := GetFavoriteSrv()
		resp, err := l.FavoriteDelete(ctx.Request.Context(), req)
		if err != nil {
			response.Fail(ctx, err)
			return
		}
		response.OK(ctx, resp)
	}
}
