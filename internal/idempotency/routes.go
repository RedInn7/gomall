package idempotency

import (
	"github.com/gin-gonic/gin"
)

// RegisterRoutes 挂载本领域路由。public 不需登录；authed 已套登录中间件；admin 已套 RequireRole("admin")。
func RegisterRoutes(public, authed, admin *gin.RouterGroup) {
	// 幂等 token 颁发
	authed.GET("idempotency/token", IdempotencyTokenHandler())
}
