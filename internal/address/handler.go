package address

import (
	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/internal/shared/response"
)

// CreateAddressHandler 新增收货地址
func CreateAddressHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		req, ok := response.Bind[AddressCreateReq](ctx)
		if !ok {
			return
		}

		l := GetAddressSrv()
		resp, err := l.AddressCreate(ctx.Request.Context(), req)
		if err != nil {
			response.Fail(ctx, err)
			return
		}
		response.OK(ctx, resp)
	}
}

// ShowAddressHandler 展示当前用户的收货地址
func ShowAddressHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		l := GetAddressSrv()
		resp, err := l.AddressShow(ctx.Request.Context())
		if err != nil {
			response.Fail(ctx, err)
			return
		}
		response.OK(ctx, resp)
	}
}

// UpdateAddressHandler 修改收货地址
func UpdateAddressHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		req, ok := response.Bind[AddressServiceReq](ctx)
		if !ok {
			return
		}

		l := GetAddressSrv()
		resp, err := l.AddressUpdate(ctx.Request.Context(), req)
		if err != nil {
			response.Fail(ctx, err)
			return
		}
		response.OK(ctx, resp)
	}
}

// DeleteAddressHandler 删除收货地址
func DeleteAddressHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		req, ok := response.Bind[AddressDeleteReq](ctx)
		if !ok {
			return
		}

		l := GetAddressSrv()
		resp, err := l.AddressDelete(ctx.Request.Context(), req)
		if err != nil {
			response.Fail(ctx, err)
			return
		}
		response.OK(ctx, resp)
	}
}
