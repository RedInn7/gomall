package order

import (
	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/middleware"
)

// RegisterRoutes 挂载本领域路由。public 不需登录；authed 已套登录中间件；merchant 已套 RequireRole("merchant"/"admin")；admin 已套 RequireRole("admin")。
func RegisterRoutes(public, authed, merchant, admin *gin.RouterGroup) {
	// 订单操作（下单走幂等）
	authed.POST("orders/create", middleware.Idempotency(), CreateOrderHandler())
	// 异步下单：MQ 削峰，前端拿 ticket 轮询 status
	authed.POST("orders/enqueue", middleware.Idempotency(), EnqueueOrderHandler())
	authed.GET("orders/status", OrderStatusHandler())
	authed.GET("orders/list", ListOrdersHandler())
	authed.GET("orders/show", ShowOrderHandler())
	authed.POST("orders/delete", DeleteOrderHandler())

	// 订单状态机扩展：履约 + 退款
	// 用户主动：确认收货 / 申请退款（幂等）
	authed.POST("orders/confirm-receive", ConfirmReceiveHandler())
	// 商家发货：挂 merchant 墙（admin 天然可过）
	merchant.POST("orders/ship", ShipOrderHandler())
}
