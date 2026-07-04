package product

import (
	"time"

	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/middleware"
)

// RegisterRoutes 挂载本领域路由。public 不需登录；authed 已套登录中间件；merchant 已套 RequireRole("merchant"/"admin")；admin 已套 RequireRole("admin")。
func RegisterRoutes(public, authed, merchant, admin *gin.RouterGroup) {
	// 商品操作
	// 公开 GET 接口挂 HTTP cache：ETag + Cache-Control 卸载浏览器/CDN 流量
	public.GET("product/list", middleware.HTTPCache(30*time.Second), ListProductsHandler())
	public.GET("product/show", middleware.HTTPCache(60*time.Second), ShowProductHandler())
	public.GET("product/imgs/list", ListProductImgHandler()) // 商品图片

	// 商品写操作：上架/改价/删除是商家动作，挂 merchant 墙（垂直越权）；
	// DAO 层 boss_id 归属条件挡商家互改（水平越权），两面墙缺一不可
	merchant.POST("product/create", CreateProductHandler())
	merchant.POST("product/update", UpdateProductHandler())
	merchant.POST("product/delete", DeleteProductHandler())
}
