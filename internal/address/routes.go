package address

import (
	"github.com/gin-gonic/gin"
)

// RegisterRoutes 挂载本领域路由。public 不需登录；authed 已套登录中间件；admin 已套 RequireRole("admin")。
func RegisterRoutes(public, authed, admin *gin.RouterGroup) {
	_ = public
	_ = admin

	// 地址操作
	authed.POST("addresses/create", CreateAddressHandler())
	authed.GET("addresses/show", ShowAddressHandler())
	authed.POST("addresses/update", UpdateAddressHandler())
	authed.POST("addresses/delete", DeleteAddressHandler())
}
