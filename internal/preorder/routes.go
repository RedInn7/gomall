package preorder

import (
	"github.com/gin-gonic/gin"
)

// RegisterRoutes 挂载本领域路由。public 不需登录；authed 已套登录中间件；merchant 已套 RequireRole("merchant"/"admin")；admin 已套 RequireRole("admin")。
//
// 接口契约：
//
//	GET  /preorder/:id          公开预售信息（:id=productID）（不需登录）
//	POST /preorder/:id/deposit  付定金（authed）
//	POST /preorder/:id/final      付尾款（authed）
//	POST /preorder/:id/cancel     定金期内取消（authed）
func RegisterRoutes(public, authed, merchant, admin *gin.RouterGroup) {
	_ = admin

	public.GET("/preorder/:id", PreorderShowHandler())
	authed.POST("/preorder/:id/deposit", PreorderDepositHandler())
	authed.POST("/preorder/:id/final", PreorderFinalHandler())
	authed.POST("/preorder/:id/cancel", PreorderCancelHandler())
}
