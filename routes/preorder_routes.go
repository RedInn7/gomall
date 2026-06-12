package routes

import (
	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/internal/preorder"
)

// RegisterPreorderRoutes 把预售四个接口挂到调用方传入的 public / authed 组。
// 不直接挂到 routes/router.go，让上层根据上线节奏选择启用时机（feature flag）。
//
// 接口契约：
//
//	GET  /preorder/:id          公开预售信息（:id=productID）（不需登录）
//	POST /preorder/:id/deposit  付定金（authed）
//	POST /preorder/:id/final      付尾款（authed）
//	POST /preorder/:id/cancel     定金期内取消（authed）
//
// 后续接入：在 routes/router.go 的相应位置调用本函数即可，例如：
//
//	RegisterPreorderRoutes(v1, authed)
func RegisterPreorderRoutes(public *gin.RouterGroup, authed *gin.RouterGroup) {
	public.GET("/preorder/:id", preorder.PreorderShowHandler())
	authed.POST("/preorder/:id/deposit", preorder.PreorderDepositHandler())
	authed.POST("/preorder/:id/final", preorder.PreorderFinalHandler())
	authed.POST("/preorder/:id/cancel", preorder.PreorderCancelHandler())
}
