package routes

import (
	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/internal/promo"
)

// RegisterPromoRoutes 注册满减引擎相关路由。
// 三组分别对应：
//
//	public —— 不需登录，结算前展示用（高频读，挂 HTTP cache 由调用方决定）
//	authed —— 当前没有用户态接口；保留参数避免后续返工
//	admin  —— 运营 / 平台后台
//
// 调用方负责按全局中间件链拼接（router.go 由父任务整合）。
func RegisterPromoRoutes(public, authed, admin *gin.RouterGroup) {
	_ = authed // 占位：当前满减引擎对用户透明，无单独用户态接口
	if public != nil {
		public.POST("/promo/calculate", promo.PromoCalculateHandler())
	}
	if admin != nil {
		admin.GET("/promo/rules", promo.AdminListPromoRulesHandler())
		admin.POST("/promo/rules", promo.AdminCreatePromoRuleHandler())
		admin.PATCH("/promo/rules/:id/stop", promo.AdminStopPromoRuleHandler())
	}
}
