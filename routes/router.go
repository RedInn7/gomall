package routes

import (
	"net/http"
	"time"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"

	api "github.com/RedInn7/gomall/api/v1"
	conf "github.com/RedInn7/gomall/config"
	"github.com/RedInn7/gomall/middleware"
)

// NewRouter 路由配置
func NewRouter() *gin.Engine {
	r := gin.Default()
	store := cookie.NewStore([]byte(conf.Config.EncryptSecret.SessionSecret))
	// 全局令牌桶：每 IP 100 RPS、突发 200，挡正常流量同时防爬虫脚本
	r.Use(middleware.TokenBucket(rate.Limit(100), 200))
	r.Use(middleware.Cors(), middleware.Jaeger())
	r.Use(sessions.Sessions("mysession", store))
	r.StaticFS("/static", http.Dir("./static"))
	v1 := r.Group("api/v1")
	{

		v1.GET("ping", func(c *gin.Context) {
			c.JSON(200, "success")
		})

		// 用户操作
		v1.POST("user/register", api.UserRegisterHandler())
		v1.POST("user/login", api.UserLoginHandler())

		// 商品操作
		v1.GET("product/list", api.ListProductsHandler())
		v1.GET("product/show", api.ShowProductHandler())
		v1.POST("product/search", api.SearchProductsHandler())
		v1.GET("product/imgs/list", api.ListProductImgHandler()) // 商品图片
		v1.GET("category/list", api.ListCategoryHandler())       // 商品分类
		v1.GET("carousels", api.ListCarouselsHandler())          // 轮播图

		authed := v1.Group("/") // 需要登陆保护
		authed.Use(middleware.AuthMiddleware())
		{

			// 用户操作
			authed.POST("user/update", api.UserUpdateHandler())
			authed.GET("user/show_info", api.ShowUserInfoHandler())
			authed.POST("user/send_email", api.SendEmailHandler())
			authed.GET("user/valid_email", api.ValidEmailHandler())
			authed.POST("user/following", api.UserFollowingHandler())
			authed.POST("user/unfollowing", api.UserUnFollowingHandler())
			authed.POST("user/avatar", api.UploadAvatarHandler()) // 上传头像

			// 商品操作
			authed.POST("product/create", api.CreateProductHandler())
			authed.POST("product/update", api.UpdateProductHandler())
			authed.POST("product/delete", api.DeleteProductHandler())
			// 收藏夹
			authed.GET("favorites/list", api.ListFavoritesHandler())
			authed.POST("favorites/create", api.CreateFavoriteHandler())
			authed.POST("favorites/delete", api.DeleteFavoriteHandler())

			// 优惠券
			authed.POST("coupon/batch", api.CreateCouponBatchHandler())
			authed.GET("coupon/batches", api.ListCouponBatchHandler())
			authed.POST("coupon/claim", api.ClaimCouponHandler())
			authed.GET("coupon/my", api.ListMyCouponHandler())

			// 幂等 token 颁发
			authed.GET("idempotency/token", api.IdempotencyTokenHandler())

			// 订单操作（下单走幂等）
			authed.POST("orders/create", middleware.Idempotency(), api.CreateOrderHandler())
			// 异步下单：MQ 削峰，前端拿 ticket 轮询 status
			authed.POST("orders/enqueue", middleware.Idempotency(), api.EnqueueOrderHandler())
			authed.GET("orders/status", api.OrderStatusHandler())
			authed.GET("orders/list", api.ListOrdersHandler())
			authed.GET("orders/old/list", api.ListOrdersHandlerOld())
			authed.GET("orders/show", api.ShowOrderHandler())
			authed.POST("orders/delete", api.DeleteOrderHandler())

			// 购物车
			authed.POST("carts/create", api.CreateCartHandler())
			authed.GET("carts/list", api.ListCartHandler())
			authed.POST("carts/update", api.UpdateCartHandler()) // 购物车id
			authed.POST("carts/delete", api.DeleteCartHandler())

			// 地址操作
			authed.POST("addresses/create", api.CreateAddressHandler())
			authed.GET("addresses/show", api.ShowAddressHandler())
			authed.POST("addresses/update", api.UpdateAddressHandler())
			authed.POST("addresses/delete", api.DeleteAddressHandler())

			// 支付功能：熔断保护下游 + 幂等防重复扣款
			authed.POST("paydown",
				middleware.CircuitBreaker(middleware.CircuitBreakerOption{
					FailureThreshold: 5,
					OpenTimeout:      10 * time.Second,
					HalfOpenMaxReq:   3,
				}),
				middleware.Idempotency(),
				api.OrderPaymentHandler())

			// 显示金额
			authed.POST("money", api.ShowMoneyHandler())

			// 秒杀专场：分布式滑动窗口限流，单用户 1s 内最多 3 次
			authed.POST("skill_product/init", api.InitSkillProductHandler())
			authed.GET("skill_product/list", api.ListSkillProductHandler())
			authed.GET("skill_product/show", api.GetSkillProductHandler())
			authed.POST("skill_product/skill",
				middleware.SlidingWindow(middleware.SlidingWindowOption{
					Scope:  "seckill",
					Window: time.Second,
					Limit:  3,
					ByUser: true,
				}),
				api.SkillProductHandler())

			// 初始 admin 引导（仅在系统无 admin 时可用）
			authed.POST("admin/bootstrap", api.BootstrapAdminHandler())

			// 管理员后台
			admin := authed.Group("/admin")
			admin.Use(middleware.RequireRole("admin"))
			{
				admin.GET("users", api.AdminListUsersHandler())
				admin.POST("users/promote", api.AdminPromoteUserHandler())
				admin.POST("search/backfill", api.AdminBackfillProductIndexHandler())
			}
		}
	}
	return r
}
