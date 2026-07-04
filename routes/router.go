package routes

import (
	"context"
	"net/http"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"

	conf "github.com/RedInn7/gomall/config"
	"github.com/RedInn7/gomall/internal/address"
	"github.com/RedInn7/gomall/internal/admin"
	"github.com/RedInn7/gomall/internal/carousel"
	"github.com/RedInn7/gomall/internal/cart"
	"github.com/RedInn7/gomall/internal/category"
	"github.com/RedInn7/gomall/internal/coupon"
	"github.com/RedInn7/gomall/internal/favorite"
	"github.com/RedInn7/gomall/internal/groupbuy"
	"github.com/RedInn7/gomall/internal/idempotency"
	"github.com/RedInn7/gomall/internal/money"
	"github.com/RedInn7/gomall/internal/order"
	"github.com/RedInn7/gomall/internal/payment"
	"github.com/RedInn7/gomall/internal/preorder"
	"github.com/RedInn7/gomall/internal/product"
	"github.com/RedInn7/gomall/internal/promo"
	"github.com/RedInn7/gomall/internal/redpacket"
	"github.com/RedInn7/gomall/internal/refund"
	"github.com/RedInn7/gomall/internal/skill"
	"github.com/RedInn7/gomall/internal/user"
	"github.com/RedInn7/gomall/middleware"
	"github.com/RedInn7/gomall/service/search"
)

// NewRouter 组合根：只负责全局中间件、分组与各领域路由的挂载。
// 具体路由定义在各领域包的 routes.go（RegisterRoutes），与领域代码同生共死。
func NewRouter() *gin.Engine {
	// RBAC 角色查询在组合根注入：middleware 不反向依赖 user 领域包（避免 import 环）
	middleware.SetRoleLookup(func(ctx context.Context, userId uint) (string, error) {
		u, err := user.NewUserDao(ctx).GetUserById(userId)
		if err != nil {
			return "", err
		}
		return u.Role, nil
	})

	r := gin.Default()
	store := cookie.NewStore([]byte(conf.Config.EncryptSecret.SessionSecret))
	// 全局令牌桶：每 IP 100 RPS、突发 200，挡正常流量同时防爬虫脚本
	r.Use(middleware.TokenBucket(rate.Limit(100), 200))
	r.Use(middleware.Cors(), middleware.Jaeger())
	r.Use(sessions.Sessions("mysession", store))
	r.StaticFS("/static", http.Dir("./static"))

	// storefront：ATELIER 前端（Vite + React 构建产物），挂在 /app 下；
	// base=/app/ 让产物内的资源引用正确解析，根路径重定向过去。
	r.Static("/app", "./web/dist")
	r.GET("/", func(c *gin.Context) {
		c.Redirect(http.StatusFound, "/app/")
	})

	v1 := r.Group("api/v1")
	v1.GET("ping", func(c *gin.Context) {
		c.JSON(200, "success")
	})

	// 四层墙，逐层收紧：public（匿名）→ authed（登录）→ merchant（商家/运营）→ admin（管理员）。
	// merchant 不加路径前缀：商家接口沿用原 URL（orders/ship 等），只是多叠一层 RBAC；
	// admin 走 /admin 前缀，语义是独立后台。admin 角色天然包含 merchant 权限。
	authed := v1.Group("/")
	authed.Use(middleware.AuthMiddleware())
	merchantGroup := authed.Group("/")
	merchantGroup.Use(middleware.RequireRole(user.RoleMerchant, user.RoleAdmin))
	adminGroup := authed.Group("/admin")
	adminGroup.Use(middleware.RequireRole(user.RoleAdmin))

	// 各领域自注册，统一签名 RegisterRoutes(public, authed, merchant, admin)。
	for _, register := range []func(public, authed, merchant, admin *gin.RouterGroup){
		user.RegisterRoutes,
		product.RegisterRoutes,
		search.RegisterRoutes,
		category.RegisterRoutes,
		carousel.RegisterRoutes,
		favorite.RegisterRoutes,
		coupon.RegisterRoutes,
		redpacket.RegisterRoutes,
		idempotency.RegisterRoutes,
		order.RegisterRoutes,
		refund.RegisterRoutes,
		cart.RegisterRoutes,
		address.RegisterRoutes,
		payment.RegisterRoutes,
		money.RegisterRoutes,
		skill.RegisterRoutes,
		admin.RegisterRoutes,
		promo.RegisterRoutes,
		groupbuy.RegisterRoutes,
		preorder.RegisterRoutes,
	} {
		register(v1, authed, merchantGroup, adminGroup)
	}

	return r
}
