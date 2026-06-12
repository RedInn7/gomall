package refund

import (
	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/middleware"
)

// RegisterRoutes 挂载本领域路由。public 不需登录；authed 已套登录中间件；admin 已套 RequireRole("admin")。
func RegisterRoutes(public, authed, admin *gin.RouterGroup) {
	// 用户主动：确认收货 / 申请退款（幂等）
	authed.POST("orders/refund/request", middleware.Idempotency(), RequestRefundHandler())
	// 商家 / 运营：发货 / 同意 / 驳回退款。merchant 角色未落地前先挂 admin RBAC
	authed.POST("orders/refund/approve", middleware.RequireRole("admin"), ApproveRefundHandler())
	authed.POST("orders/refund/reject", middleware.RequireRole("admin"), RejectRefundHandler())
}
