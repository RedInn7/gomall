package skill

import (
	"time"

	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/middleware"
)

// RegisterRoutes 挂载本领域路由。public 不需登录；authed 已套登录中间件；merchant 已套 RequireRole("merchant"/"admin")；admin 已套 RequireRole("admin")。
func RegisterRoutes(public, authed, merchant, admin *gin.RouterGroup) {
	// 创建 + 预热秒杀活动是运营动作，只给 admin；按 (product_id, start_at) 幂等
	admin.POST("skill_product/init", InitSkillProductHandler())
	authed.GET("skill_product/list", ListSkillProductHandler())
	authed.GET("skill_product/show", GetSkillProductHandler())
	// 秒杀下单：分布式滑动窗口限流（单用户 1s 内最多 3 次）+ 幂等键防重复提交
	authed.POST("skill_product/skill",
		middleware.SlidingWindow(middleware.SlidingWindowOption{
			Scope:  "seckill",
			Window: time.Second,
			Limit:  3,
			ByUser: true,
		}),
		middleware.Idempotency(),
		SkillProductHandler())
}
