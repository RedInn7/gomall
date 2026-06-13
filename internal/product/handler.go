package product

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/internal/shared/response"
)

// CreateProductHandler 创建商品
func CreateProductHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		req, ok := response.Bind[ProductCreateReq](ctx)
		if !ok {
			return
		}

		form, err := ctx.MultipartForm()
		if err != nil {
			response.Fail(ctx, err)
			return
		}
		files := form.File["image"]
		l := GetProductSrv()
		resp, err := l.ProductCreate(ctx.Request.Context(), files, req)
		if err != nil {
			response.Fail(ctx, err)
			return
		}
		response.OK(ctx, resp)
	}
}

// ListProductsHandler 商品列表
func ListProductsHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		req, ok := response.Bind[ProductListReq](ctx)
		if !ok {
			return
		}
		if req.PageSize == 0 {
			req.PageSize = consts.BaseProductPageSize
		}

		l := GetProductSrv()
		resp, err := l.ProductList(ctx.Request.Context(), req)
		if err != nil {
			response.Fail(ctx, err)
			return
		}
		response.OK(ctx, resp)
	}
}

// ShowProductHandler 商品详情
func ShowProductHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		req, ok := response.Bind[ProductShowReq](ctx)
		if !ok {
			return
		}

		l := GetProductSrv()
		resp, err := l.ProductShow(ctx.Request.Context(), req)
		if err != nil {
			response.Fail(ctx, err)
			return
		}
		response.OK(ctx, resp)
	}
}

// DeleteProductHandler 删除商品
func DeleteProductHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		req, ok := response.Bind[ProductDeleteReq](ctx)
		if !ok {
			return
		}

		l := GetProductSrv()
		resp, err := l.ProductDelete(ctx.Request.Context(), req)
		if err != nil {
			response.Fail(ctx, err)
			return
		}
		response.OK(ctx, resp)
	}
}

// UpdateProductHandler 更新商品
func UpdateProductHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		req, ok := response.Bind[ProductUpdateReq](ctx)
		if !ok {
			return
		}

		l := GetProductSrv()
		resp, err := l.ProductUpdate(ctx.Request.Context(), req)
		if err != nil {
			response.Fail(ctx, err)
			return
		}
		response.OK(ctx, resp)
	}
}

func ListProductImgHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		req, ok := response.Bind[ListProductImgReq](ctx)
		if !ok {
			return
		}
		if req.ID == 0 {
			err := errors.New("参数错误,id不能为空")
			ctx.JSON(http.StatusOK, response.ErrorResponse(ctx, err))
			return
		}

		l := GetProductSrv()
		resp, err := l.ProductImgList(ctx.Request.Context(), req)
		if err != nil {
			response.Fail(ctx, err)
			return
		}
		response.OK(ctx, resp)
	}
}
