package coupon

import (
	"github.com/gin-gonic/gin"
)

// RegisterRoutes 挂载本领域路由。public 不需登录；authed 已套登录中间件；admin 已套 RequireRole("admin")。
func RegisterRoutes(public, authed, admin *gin.RouterGroup) {
	// 优惠券
	authed.POST("coupon/batch", CreateCouponBatchHandler())
	authed.GET("coupon/batches", ListCouponBatchHandler())
	authed.POST("coupon/claim", ClaimCouponHandler())
	authed.GET("coupon/my", ListMyCouponHandler())
}
