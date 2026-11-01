package groupbuy

import (
	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/middleware"
)

// RegisterRoutes 挂载本领域路由。public 不需登录；authed 已套登录中间件；merchant 已套 RequireRole("merchant"/"admin")；admin 已套 RequireRole("admin")。
//
//	public 组：分享落地页 GET /groupbuy/:id 不要鉴权（裂变流量主要靠未登录浏览）
//	authed 组：发起 / 加入需要鉴权 + 走幂等中间件，避免重复发团 / 重复点"加入"
//	admin 组：运营 / 风控手动关团，关团即原路退款
func RegisterRoutes(public, authed, merchant, admin *gin.RouterGroup) {
	public.GET("/groupbuy/:id", GroupbuyShowHandler())
	authed.POST("/groupbuy/create", middleware.Idempotency(), GroupbuyCreateHandler())
	authed.POST("/groupbuy/:id/join", middleware.Idempotency(), GroupbuyJoinHandler())

	admin.POST("groupbuy/:id/close", AdminCloseGroupHandler())
}
