package skill

import (
	"time"

	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/middleware"
)

// RegisterRoutes 挂载本领域路由。public 不需登录；authed 已套登录中间件；admin 已套 RequireRole("admin")。
func RegisterRoutes(public, authed, admin *gin.RouterGroup) {
	// 秒杀专场：分布式滑动窗口限流，单用户 1s 内最多 3 次
	authed.POST("skill_product/init", InitSkillProductHandler())
	authed.GET("skill_product/list", ListSkillProductHandler())
	authed.GET("skill_product/show", GetSkillProductHandler())
	authed.POST("skill_product/skill",
		middleware.SlidingWindow(middleware.SlidingWindowOption{
			Scope:  "seckill",
			Window: time.Second,
			Limit:  3,
			ByUser: true,
		}),
		SkillProductHandler())
}
