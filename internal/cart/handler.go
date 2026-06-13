package cart

import (
	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/internal/shared/response"
)

// CreateCartHandler 加入购物车
func CreateCartHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		req, ok := response.Bind[CartCreateReq](ctx)
		if !ok {
			return
		}

		l := GetCartSrv()
		resp, err := l.CartCreate(ctx.Request.Context(), req)
		if err != nil {
			response.Fail(ctx, err)
			return
		}
		response.OK(ctx, resp)
	}
}

// ListCartHandler 购物车详细信息
func ListCartHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		req, ok := response.Bind[CartListReq](ctx)
		if !ok {
			return
		}
		if req.PageSize == 0 {
			req.PageSize = consts.BasePageSize
		}

		l := GetCartSrv()
		resp, err := l.CartList(ctx.Request.Context(), req)
		if err != nil {
			response.Fail(ctx, err)
			return
		}
		response.OK(ctx, resp)
	}
}

// UpdateCartHandler 修改购物车信息
func UpdateCartHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		req, ok := response.Bind[UpdateCartServiceReq](ctx)
		if !ok {
			return
		}

		l := GetCartSrv()
		resp, err := l.CartUpdate(ctx.Request.Context(), req)
		if err != nil {
			response.Fail(ctx, err)
			return
		}
		response.OK(ctx, resp)
	}
}

// DeleteCartHandler 删除购物车
func DeleteCartHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		req, ok := response.Bind[CartDeleteReq](ctx)
		if !ok {
			return
		}

		l := GetCartSrv()
		resp, err := l.CartDelete(ctx.Request.Context(), req)
		if err != nil {
			response.Fail(ctx, err)
			return
		}
		response.OK(ctx, resp)
	}
}
