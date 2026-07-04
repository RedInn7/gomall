package carousel

import (
	"time"

	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/middleware"
)

// RegisterRoutes 挂载本领域路由。public 不需登录；authed 已套登录中间件；merchant 已套 RequireRole("merchant"/"admin")；admin 已套 RequireRole("admin")。
func RegisterRoutes(public, authed, merchant, admin *gin.RouterGroup) {
	public.GET("carousels", middleware.HTTPCache(300*time.Second), ListCarouselsHandler()) // 轮播图
}
