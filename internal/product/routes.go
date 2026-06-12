package product

import (
	"time"

	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/middleware"
)

// RegisterRoutes 挂载本领域路由。public 不需登录；authed 已套登录中间件；admin 已套 RequireRole("admin")。
func RegisterRoutes(public, authed, admin *gin.RouterGroup) {
	// 商品操作
	// 公开 GET 接口挂 HTTP cache：ETag + Cache-Control 卸载浏览器/CDN 流量
	public.GET("product/list", middleware.HTTPCache(30*time.Second), ListProductsHandler())
	public.GET("product/show", middleware.HTTPCache(60*time.Second), ShowProductHandler())
	public.GET("product/imgs/list", ListProductImgHandler()) // 商品图片

	// 商品操作
	authed.POST("product/create", CreateProductHandler())
	authed.POST("product/update", UpdateProductHandler())
	authed.POST("product/delete", DeleteProductHandler())
}
