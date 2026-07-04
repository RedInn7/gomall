package cart

import (
	"github.com/gin-gonic/gin"
)

// RegisterRoutes 挂载本领域路由。public 不需登录；authed 已套登录中间件；merchant 已套 RequireRole("merchant"/"admin")；admin 已套 RequireRole("admin")。
func RegisterRoutes(public, authed, merchant, admin *gin.RouterGroup) {
	_ = public
	_ = admin

	// 购物车
	authed.POST("carts/create", CreateCartHandler())
	authed.GET("carts/list", ListCartHandler())
	authed.POST("carts/update", UpdateCartHandler()) // 购物车id
	authed.POST("carts/delete", DeleteCartHandler())
}
