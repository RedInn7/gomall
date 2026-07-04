package promo

import (
	"github.com/gin-gonic/gin"
)

// RegisterRoutes 挂载本领域路由。public 不需登录；authed 已套登录中间件；merchant 已套 RequireRole("merchant"/"admin")；admin 已套 RequireRole("admin")。
// 三组分别对应：
//
//	public —— 不需登录，结算前展示用（高频读，挂 HTTP cache 由调用方决定）
//	authed —— 当前没有用户态接口；保留参数避免后续返工
//	admin  —— 运营 / 平台后台
func RegisterRoutes(public, authed, merchant, admin *gin.RouterGroup) {
	_ = authed // 占位：当前满减引擎对用户透明，无单独用户态接口
	if public != nil {
		public.POST("/promo/calculate", PromoCalculateHandler())
	}
	if admin != nil {
		admin.GET("/promo/rules", AdminListPromoRulesHandler())
		admin.POST("/promo/rules", AdminCreatePromoRuleHandler())
		admin.PATCH("/promo/rules/:id/stop", AdminStopPromoRuleHandler())
	}
}
