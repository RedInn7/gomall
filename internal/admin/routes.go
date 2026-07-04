package admin

import (
	"github.com/gin-gonic/gin"
)

// RegisterRoutes 挂载本领域路由。public 不需登录；authed 已套登录中间件；merchant 已套 RequireRole("merchant"/"admin")；admin 已套 RequireRole("admin")。
func RegisterRoutes(public, authed, merchant, admin *gin.RouterGroup) {
	// 初始 admin 引导（仅在系统无 admin 时可用）
	authed.POST("admin/bootstrap", BootstrapAdminHandler())

	// 管理员后台
	admin.GET("users", AdminListUsersHandler())
	admin.POST("users/promote", AdminPromoteUserHandler())
	admin.POST("search/backfill", AdminBackfillProductIndexHandler())
}
