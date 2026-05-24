package routes

import (
	"github.com/gin-gonic/gin"

	api "github.com/RedInn7/gomall/api/v1"
)

// RegisterPreorderRoutes 把预售四个接口挂到调用方传入的 public / authed 组。
// 不直接挂到 routes/router.go，让上层根据上线节奏选择启用时机（feature flag）。
//
// 接口契约：
//   GET  /preorder/:productID          公开预售信息（不需登录）
//   POST /preorder/:productID/deposit  付定金（authed）
//   POST /preorder/:orderID/final      付尾款（authed）
//   POST /preorder/:orderID/cancel     定金期内取消（authed）
//
// 后续接入：在 routes/router.go 的相应位置调用本函数即可，例如：
//   RegisterPreorderRoutes(v1, authed)
func RegisterPreorderRoutes(public *gin.RouterGroup, authed *gin.RouterGroup) {
	public.GET("/preorder/:productID", api.PreorderShowHandler())
	authed.POST("/preorder/:productID/deposit", api.PreorderDepositHandler())
	authed.POST("/preorder/:orderID/final", api.PreorderFinalHandler())
	authed.POST("/preorder/:orderID/cancel", api.PreorderCancelHandler())
}
