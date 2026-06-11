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
	"github.com/RedInn7/gomall/internal/address"
	adminapi "github.com/RedInn7/gomall/internal/admin"
	"github.com/RedInn7/gomall/internal/carousel"
	"github.com/RedInn7/gomall/internal/cart"
	"github.com/RedInn7/gomall/internal/category"
	"github.com/RedInn7/gomall/internal/coupon"
	"github.com/RedInn7/gomall/internal/favorite"
	"github.com/RedInn7/gomall/internal/money"
	"github.com/RedInn7/gomall/internal/order"
	"github.com/RedInn7/gomall/internal/product"
	"github.com/RedInn7/gomall/internal/redpacket"
	"github.com/RedInn7/gomall/internal/skill"
	"github.com/RedInn7/gomall/internal/user"
	"github.com/RedInn7/gomall/middleware"
	"github.com/RedInn7/gomall/service/search"
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
		v1.POST("user/register", user.UserRegisterHandler())
		v1.POST("user/login", user.UserLoginHandler())

		// 商品操作
		// 公开 GET 接口挂 HTTP cache：ETag + Cache-Control 卸载浏览器/CDN 流量
		v1.GET("product/list", middleware.HTTPCache(30*time.Second), product.ListProductsHandler())
		v1.GET("product/show", middleware.HTTPCache(60*time.Second), product.ShowProductHandler())
		v1.POST("product/search", search.SearchProductsHandler())
		v1.POST("product/semantic-search", search.SemanticSearchProductsHandler())                     // 语义检索: ES + Milvus 融合
		v1.GET("product/imgs/list", product.ListProductImgHandler())                                   // 商品图片
		v1.GET("category/list", middleware.HTTPCache(300*time.Second), category.ListCategoryHandler()) // 商品分类
		v1.GET("carousels", middleware.HTTPCache(300*time.Second), carousel.ListCarouselsHandler())    // 轮播图

		authed := v1.Group("/") // 需要登陆保护
		authed.Use(middleware.AuthMiddleware())
		{

			// 用户操作
			authed.POST("user/update", user.UserUpdateHandler())
			authed.GET("user/show_info", user.ShowUserInfoHandler())
			authed.POST("user/send_email", user.SendEmailHandler())
			authed.GET("user/valid_email", user.ValidEmailHandler())
			authed.POST("user/following", user.UserFollowingHandler())
			authed.POST("user/unfollowing", user.UserUnFollowingHandler())
			authed.POST("user/avatar", user.UploadAvatarHandler()) // 上传头像

			// 商品操作
			authed.POST("product/create", product.CreateProductHandler())
			authed.POST("product/update", product.UpdateProductHandler())
			authed.POST("product/delete", product.DeleteProductHandler())
			// 收藏夹
			authed.GET("favorites/list", favorite.ListFavoritesHandler())
			authed.POST("favorites/create", favorite.CreateFavoriteHandler())
			authed.POST("favorites/delete", favorite.DeleteFavoriteHandler())

			// 优惠券
			authed.POST("coupon/batch", coupon.CreateCouponBatchHandler())
			authed.GET("coupon/batches", coupon.ListCouponBatchHandler())
			authed.POST("coupon/claim", coupon.ClaimCouponHandler())
			authed.GET("coupon/my", coupon.ListMyCouponHandler())

			// 红包：发包幂等；抢包按用户做滑动窗口 + 幂等
			authed.POST("redpacket/create",
				middleware.Idempotency(),
				redpacket.CreateRedPacketHandler())
			authed.POST("redpacket/claim",
				middleware.SlidingWindow(middleware.SlidingWindowOption{
					Scope: "redpacket", Window: time.Second, Limit: 3, ByUser: true,
				}),
				middleware.Idempotency(),
				redpacket.ClaimRedPacketHandler())
			authed.GET("redpacket/show", redpacket.ShowRedPacketHandler())
			authed.GET("redpacket/list", redpacket.ListMyRedPacketsHandler())

			// 幂等 token 颁发
			authed.GET("idempotency/token", api.IdempotencyTokenHandler())

			// 订单操作（下单走幂等）
			authed.POST("orders/create", middleware.Idempotency(), order.CreateOrderHandler())
			// 异步下单：MQ 削峰，前端拿 ticket 轮询 status
			authed.POST("orders/enqueue", middleware.Idempotency(), order.EnqueueOrderHandler())
			authed.GET("orders/status", order.OrderStatusHandler())
			authed.GET("orders/list", order.ListOrdersHandler())
			authed.GET("orders/old/list", order.ListOrdersHandlerOld())
			authed.GET("orders/show", order.ShowOrderHandler())
			authed.POST("orders/delete", order.DeleteOrderHandler())

			// 订单状态机扩展：履约 + 退款
			// 用户主动：确认收货 / 申请退款（幂等）
			authed.POST("orders/confirm-receive", order.ConfirmReceiveHandler())
			authed.POST("orders/refund/request", middleware.Idempotency(), api.RequestRefundHandler())
			// 商家 / 运营：发货 / 同意 / 驳回退款。merchant 角色未落地前先挂 admin RBAC
			authed.POST("orders/ship", middleware.RequireRole("admin"), order.ShipOrderHandler())
			authed.POST("orders/refund/approve", middleware.RequireRole("admin"), api.ApproveRefundHandler())
			authed.POST("orders/refund/reject", middleware.RequireRole("admin"), api.RejectRefundHandler())

			// 购物车
			authed.POST("carts/create", cart.CreateCartHandler())
			authed.GET("carts/list", cart.ListCartHandler())
			authed.POST("carts/update", cart.UpdateCartHandler()) // 购物车id
			authed.POST("carts/delete", cart.DeleteCartHandler())

			// 地址操作
			authed.POST("addresses/create", address.CreateAddressHandler())
			authed.GET("addresses/show", address.ShowAddressHandler())
			authed.POST("addresses/update", address.UpdateAddressHandler())
			authed.POST("addresses/delete", address.DeleteAddressHandler())

			// 支付功能：熔断保护下游 + 幂等防重复扣款
			authed.POST("paydown",
				middleware.CircuitBreaker(middleware.CircuitBreakerOption{
					FailureThreshold: 5,
					OpenTimeout:      10 * time.Second,
					HalfOpenMaxReq:   3,
				}),
				middleware.Idempotency(),
				api.OrderPaymentHandler())

			// Web3 钱包签名支付：先取 nonce，再带签名提交。链上确认由 listener 兜底
			authed.GET("paydown/crypto/nonce", api.CryptoPaydownNonceHandler())
			authed.POST("paydown/crypto",
				middleware.Idempotency(),
				api.CryptoPaydownHandler())

			// 显示金额
			authed.POST("money", money.ShowMoneyHandler())

			// 秒杀专场：分布式滑动窗口限流，单用户 1s 内最多 3 次
			authed.POST("skill_product/init", skill.InitSkillProductHandler())
			authed.GET("skill_product/list", skill.ListSkillProductHandler())
			authed.GET("skill_product/show", skill.GetSkillProductHandler())
			authed.POST("skill_product/skill",
				middleware.SlidingWindow(middleware.SlidingWindowOption{
					Scope:  "seckill",
					Window: time.Second,
					Limit:  3,
					ByUser: true,
				}),
				skill.SkillProductHandler())

			// 初始 admin 引导（仅在系统无 admin 时可用）
			authed.POST("admin/bootstrap", adminapi.BootstrapAdminHandler())

			// 管理员后台
			admin := authed.Group("/admin")
			admin.Use(middleware.RequireRole("admin"))
			{
				admin.GET("users", adminapi.AdminListUsersHandler())
				admin.POST("users/promote", adminapi.AdminPromoteUserHandler())
				admin.POST("search/backfill", adminapi.AdminBackfillProductIndexHandler())
			}

			// 新业务域：满减 / 拼团 / 预售。三组拆到独立 routes/*_routes.go
			// 是为了降 merge conflict + 让每个 feature 能单独构造 router 跑 E2E
			RegisterPromoRoutes(v1, authed, admin)
			RegisterGroupbuyRoutes(v1, authed)
			RegisterPreorderRoutes(v1, authed)
		}
	}
	return r
}
