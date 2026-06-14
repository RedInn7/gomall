package payment

import (
	"time"

	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/middleware"
)

// RegisterRoutes 挂载本领域路由。public 不需登录；authed 已套登录中间件；admin 已套 RequireRole("admin")。
func RegisterRoutes(public, authed, admin *gin.RouterGroup) {
	// 支付功能：熔断保护下游 + 幂等防重复扣款
	authed.POST("paydown",
		middleware.CircuitBreaker(middleware.CircuitBreakerOption{
			FailureThreshold: 5,
			OpenTimeout:      10 * time.Second,
			HalfOpenMaxReq:   3,
		}),
		middleware.Idempotency(),
		OrderPaymentHandler())

	// Web3 钱包签名支付：先取 nonce，再带签名提交。链上确认由 listener 兜底
	authed.GET("paydown/crypto/nonce", CryptoPaydownNonceHandler())
	authed.POST("paydown/crypto",
		middleware.Idempotency(),
		CryptoPaydownHandler())

	// Stripe 托管支付：登录后创建 Checkout Session，跳转 Stripe 完成支付
	authed.POST("paydown/stripe",
		middleware.Idempotency(),
		StripeCheckoutHandler())
	// Stripe webhook：公开端点（无登录），靠签名校验来源；支付完成后由它兜底结算订单
	public.POST("webhooks/stripe", StripeWebhookHandler())
}
