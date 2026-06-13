package order

import (
	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/internal/shared/response"
)

// EnqueueOrderHandler 异步下单：reserve 库存 + 投 MQ，立即返回 ticket
func EnqueueOrderHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		req, ok := response.Bind[OrderCreateReq](ctx)
		if !ok {
			return
		}

		l := GetOrderSrv()
		resp, err := l.OrderEnqueue(ctx.Request.Context(), req)
		if err != nil {
			response.Fail(ctx, err)
			return
		}
		response.OK(ctx, resp)
	}
}

// OrderStatusHandler 通过 ticket 查询异步下单结果
func OrderStatusHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		ticket := ctx.Query("ticket")
		l := GetOrderSrv()
		resp, err := l.OrderStatus(ctx.Request.Context(), ticket)
		if err != nil {
			response.Fail(ctx, err)
			return
		}
		response.OK(ctx, resp)
	}
}

func CreateOrderHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		req, ok := response.Bind[OrderCreateReq](ctx)
		if !ok {
			return
		}

		l := GetOrderSrv()
		resp, err := l.OrderCreate(ctx.Request.Context(), req)
		if err != nil {
			response.Fail(ctx, err)
			return
		}
		response.OK(ctx, resp)
	}
}

func ListOrdersHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		req, ok := response.Bind[OrderListReq](ctx)
		if !ok {
			return
		}
		if req.PageSize == 0 {
			req.PageSize = consts.BasePageSize
		}

		l := GetOrderSrv()
		resp, err := l.OrderList(ctx.Request.Context(), req)
		if err != nil {
			response.Fail(ctx, err)
			return
		}
		response.OK(ctx, resp)
	}
}

func ListOrdersHandlerOld() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		req, ok := response.Bind[OrderListReq](ctx)
		if !ok {
			return
		}
		if req.PageSize == 0 {
			req.PageSize = consts.BasePageSize
		}

		l := GetOrderSrv()
		resp, err := l.OrderListOld(ctx.Request.Context(), req)
		if err != nil {
			response.Fail(ctx, err)
			return
		}
		response.OK(ctx, resp)
	}
}

// ShowOrderHandler 订单详情
func ShowOrderHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		req, ok := response.Bind[OrderShowReq](ctx)
		if !ok {
			return
		}

		l := GetOrderSrv()
		resp, err := l.OrderShow(ctx.Request.Context(), req)
		if err != nil {
			response.Fail(ctx, err)
			return
		}
		response.OK(ctx, resp)
	}
}

func DeleteOrderHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		req, ok := response.Bind[OrderDeleteReq](ctx)
		if !ok {
			return
		}

		l := GetOrderSrv()
		resp, err := l.OrderDelete(ctx.Request.Context(), req)
		if err != nil {
			response.Fail(ctx, err)
			return
		}
		response.OK(ctx, resp)
	}
}
