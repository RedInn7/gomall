package routes

import (
	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/internal/groupbuy"
)

// RegisterGroupbuyRoutes 拼团路由注册。
//
//	public 组：分享落地页 GET /groupbuy/:id 不要鉴权（裂变流量主要靠未登录浏览）
//	authed 组：发起 / 加入需要鉴权 + 走幂等中间件，避免重复发团 / 重复点"加入"
//
// 调用方在 router.go 内：
//
//	v1Public := r.Group("api/v1")
//	authed := v1Public.Group("/")
//	authed.Use(middleware.AuthMiddleware())
//	routes.RegisterGroupbuyRoutes(v1Public, authed)
//
// 拆成独立文件而非塞到 router.go 是为了：
//  1. 新业务域上线降低 merge conflict 面积
//  2. 路由级压测 / E2E 测试可以单独构造一个 router 跑这一组
func RegisterGroupbuyRoutes(public *gin.RouterGroup, authed *gin.RouterGroup) {
	public.GET("/groupbuy/:id", groupbuy.GroupbuyShowHandler())
	authed.POST("/groupbuy/create", groupbuy.GroupbuyCreateHandler())
	authed.POST("/groupbuy/:id/join", groupbuy.GroupbuyJoinHandler())
}
