package routes

import (
	"context"
	"net/http"
	"os"
	"strings"

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
	"github.com/RedInn7/gomall/internal/clearing"
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
	// token 版本号查询同理在组合根注入：AuthMiddleware 逐请求比对实现 token 撤销
	middleware.SetTokenVersionLookup(func(ctx context.Context, userId uint) (uint, error) {
		u, err := user.NewUserDao(ctx).GetUserById(userId)
		if err != nil {
			return 0, err
		}
		return u.TokenVersion, nil
	})

	r := gin.Default()
	// gin 默认信任所有代理，客户端伪造 X-Forwarded-For 就能绕过按 IP 的限流。
	// 直连部署不信任任何代理（ClientIP 取 TCP 对端）；挂在反代 / LB 后面时用
	// TRUSTED_PROXIES 配置其 CIDR，逗号分隔。
	var trustedProxies []string
	for _, p := range strings.Split(os.Getenv("TRUSTED_PROXIES"), ",") {
		if p = strings.TrimSpace(p); p != "" {
			trustedProxies = append(trustedProxies, p)
		}
	}
	if err := r.SetTrustedProxies(trustedProxies); err != nil {
		panic("TRUSTED_PROXIES 配置非法: " + err.Error())
	}
	store := cookie.NewStore([]byte(conf.Config.EncryptSecret.SessionSecret))
	// 全局令牌桶：每 IP 100 RPS、突发 200，挡正常流量同时防爬虫脚本
	r.Use(middleware.TokenBucket(rate.Limit(100), 200))
	r.Use(middleware.Cors())
	r.Use(sessions.Sessions("mysession", store))
	// Static 不列目录（StaticFS + http.Dir 会列出目录，暴露上传文件清单）
	r.Static("/static", "./static")

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
		clearing.RegisterRoutes,
		promo.RegisterRoutes,
		groupbuy.RegisterRoutes,
		preorder.RegisterRoutes,
	} {
		register(v1, authed, merchantGroup, adminGroup)
	}

	return r
}
