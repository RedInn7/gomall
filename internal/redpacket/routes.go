package redpacket

import (
	"time"

	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/middleware"
)

// RegisterRoutes 挂载本领域路由。public 不需登录；authed 已套登录中间件；admin 已套 RequireRole("admin")。
func RegisterRoutes(public, authed, admin *gin.RouterGroup) {
	_ = public
	_ = admin

	// 红包：发包幂等；抢包按用户做滑动窗口 + 幂等
	authed.POST("redpacket/create",
		middleware.Idempotency(),
		CreateRedPacketHandler())
	authed.POST("redpacket/claim",
		middleware.SlidingWindow(middleware.SlidingWindowOption{
			Scope: "redpacket", Window: time.Second, Limit: 3, ByUser: true,
		}),
		middleware.Idempotency(),
		ClaimRedPacketHandler())
	authed.GET("redpacket/show", ShowRedPacketHandler())
	authed.GET("redpacket/list", ListMyRedPacketsHandler())
}
