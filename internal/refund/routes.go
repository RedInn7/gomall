package refund

import (
	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/middleware"
)

// RegisterRoutes 挂载本领域路由。public 不需登录；authed 已套登录中间件；merchant 已套 RequireRole("merchant"/"admin")；admin 已套 RequireRole("admin")。
func RegisterRoutes(public, authed, merchant, admin *gin.RouterGroup) {
	// 用户主动：确认收货 / 申请退款（幂等）
	authed.POST("orders/refund/request", middleware.Idempotency(), RequestRefundHandler())
	// 商家 / 运营：同意 / 驳回退款。approve 会触发真实资金结算，必须挂 merchant 墙（admin 天然可过）
	merchant.POST("orders/refund/approve", ApproveRefundHandler())
	merchant.POST("orders/refund/reject", RejectRefundHandler())
}
