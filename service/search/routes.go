package search

import (
	"github.com/gin-gonic/gin"
)

// RegisterRoutes 挂载本领域路由。public 不需登录；authed 已套登录中间件；merchant 已套 RequireRole("merchant"/"admin")；admin 已套 RequireRole("admin")。
func RegisterRoutes(public, authed, merchant, admin *gin.RouterGroup) {
	public.POST("product/search", SearchProductsHandler())
	public.POST("product/semantic-search", SemanticSearchProductsHandler()) // 语义检索: ES + Milvus 融合
}
