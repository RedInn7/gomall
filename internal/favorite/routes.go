package favorite

import (
	"github.com/gin-gonic/gin"
)

// RegisterRoutes 挂载本领域路由。public 不需登录；authed 已套登录中间件；merchant 已套 RequireRole("merchant"/"admin")；admin 已套 RequireRole("admin")。
func RegisterRoutes(public, authed, merchant, admin *gin.RouterGroup) {
	// 收藏夹
	authed.GET("favorites/list", ListFavoritesHandler())
	authed.POST("favorites/create", CreateFavoriteHandler())
	authed.POST("favorites/delete", DeleteFavoriteHandler())
}
