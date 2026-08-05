package payment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/stripe/stripe-go/v82"
	"github.com/stripe/stripe-go/v82/checkout/session"
	"github.com/stripe/stripe-go/v82/webhook"
	"gorm.io/gorm"

	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/internal/clearing"
	orderpkg "github.com/RedInn7/gomall/internal/order"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
	"github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/cache"
)

// Stripe 配置统一走环境变量（与 Web3 listener 读 WEB3_* 的方式一致），密钥不落 yaml。
// STRIPE_SECRET_KEY 推荐用受限密钥 rk_ 而非 sk_，最小权限。
const (
	envStripeSecretKey     = "STRIPE_SECRET_KEY"
	envStripeWebhookSecret = "STRIPE_WEBHOOK_SECRET"
	envStripeSuccessURL    = "STRIPE_SUCCESS_URL"
	envStripeCancelURL     = "STRIPE_CANCEL_URL"
	envStripeCurrency      = "STRIPE_CURRENCY"
)

var (
	// ErrStripeNotConfigured 未配置密钥时返回，调用方据此提示走其它支付通道。
	ErrStripeNotConfigured = errors.New("Stripe 未配置：缺少 STRIPE_SECRET_KEY")
	// ErrStripeWebhookNotConfigured webhook 校验密钥缺失。
	ErrStripeWebhookNotConfigured = errors.New("Stripe webhook 未配置：缺少 STRIPE_WEBHOOK_SECRET")
	// ErrStripeSignature 签名校验失败：请求可能被伪造或篡改，应拒绝且无需 Stripe 重投。
	ErrStripeSignature = errors.New("Stripe webhook 签名校验失败")
)

// stripeEventDedupeTTL Stripe 事件去重键的存活时长。Stripe 的重投窗口最长约 3 天，
// 取 72h 覆盖其全部自动重试，过期后键自然回收，不长期占用 Redis。
const stripeEventDedupeTTL = 72 * time.Hour

// stripeEventDedupeKey 以 event.ID 为粒度的去重键。Stripe 每个事件 ID 全局唯一，
// 同一事件无论重投多少次都落同一个键。
func stripeEventDedupeKey(eventID string) string {
	return fmt.Sprintf("stripe:event:%s", eventID)
}

func stripeSecretKey() string     { return strings.TrimSpace(os.Getenv(envStripeSecretKey)) }
func stripeWebhookSecret() string { return strings.TrimSpace(os.Getenv(envStripeWebhookSecret)) }

func stripeCurrency() string {
	if c := strings.TrimSpace(strings.ToLower(os.Getenv(envStripeCurrency))); c != "" {
		return c
	}
	return "usd"
}

func stripeSuccessURL() string {
	if u := strings.TrimSpace(os.Getenv(envStripeSuccessURL)); u != "" {
		return u
	}
	return "https://example.com/pay/success?session_id={CHECKOUT_SESSION_ID}"
}

func stripeCancelURL() string {
	if u := strings.TrimSpace(os.Getenv(envStripeCancelURL)); u != "" {
		return u
	}
	return "https://example.com/pay/cancel"
}

var (
	StripePaymentSrvIns  *StripePaymentSrv
	StripePaymentSrvOnce sync.Once
)

type StripePaymentSrv struct{}

func GetStripePaymentSrv() *StripePaymentSrv {
	StripePaymentSrvOnce.Do(func() { StripePaymentSrvIns = &StripePaymentSrv{} })
	return StripePaymentSrvIns
}

// CreateCheckout 为一笔待支付订单创建 Stripe Checkout Session，返回托管支付页 URL。
// 金额取折后实付 FinalCents（与钱包 / Web3 路径口径一致），绝不读 req 里的金额，防篡改。
func (s *StripePaymentSrv) CreateCheckout(ctx context.Context, req *StripeCheckoutReq) (*StripeCheckoutResp, error) {
	sk := stripeSecretKey()
	if sk == "" {
		return nil, ErrStripeNotConfigured
	}

	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		log.LogrusObj.Error(err)
		return nil, err
	}

	order, err := orderpkg.NewOrderDao(ctx).GetOrderById(req.OrderID, u.Id)
	if err != nil {
		log.LogrusObj.Error(err)
		return nil, err
	}
	if order.Type != consts.OrderWaitPay {
		return nil, errors.New("订单状态非未支付，无法发起支付")
	}

	// 实付口径统一走 orderPayableCents（命中促销取折后 FinalCents），与钱包 / Web3 路径一致。
	payable := orderPayableCents(order)
	if payable <= 0 {
		return nil, errors.New("订单应付金额非法")
	}

	stripe.Key = sk
	params := &stripe.CheckoutSessionParams{
		Mode:              stripe.String(string(stripe.CheckoutSessionModePayment)),
		ClientReferenceID: stripe.String(strconv.FormatUint(uint64(order.ID), 10)),
		SuccessURL:        stripe.String(stripeSuccessURL()),
		CancelURL:         stripe.String(stripeCancelURL()),
		// 不传 payment_method_types：启用动态支付方式，由 Dashboard 配置，最大化转化。
		LineItems: []*stripe.CheckoutSessionLineItemParams{
			{
				Quantity: stripe.Int64(1),
				PriceData: &stripe.CheckoutSessionLineItemPriceDataParams{
					Currency:   stripe.String(stripeCurrency()),
					UnitAmount: stripe.Int64(payable),
					ProductData: &stripe.CheckoutSessionLineItemPriceDataProductDataParams{
						Name: stripe.String("Order #" + strconv.FormatUint(order.OrderNum, 10)),
					},
				},
			},
		},
	}
	// webhook 结算所需上下文随 metadata 透传（client_reference_id 已带 order_id，这里补 user_id）。
	params.AddMetadata("order_id", strconv.FormatUint(uint64(order.ID), 10))
	params.AddMetadata("user_id", strconv.FormatUint(uint64(u.Id), 10))
	// 同一订单复用同一幂等键：用户重复点支付不会在 Stripe 侧重复建会话。
	params.SetIdempotencyKey("checkout-order-" + strconv.FormatUint(uint64(order.ID), 10))

	sess, err := session.New(params)
	if err != nil {
		log.LogrusObj.Errorf("stripe create checkout session failed order=%d err=%v", order.ID, err)
		return nil, err
	}
	return &StripeCheckoutResp{SessionID: sess.ID, URL: sess.URL}, nil
}

// HandleWebhook 校验 Stripe 签名并处理事件。仅 checkout.session.completed 且已支付时结算订单。
// 返回 error 表示需要 Stripe 重投（5xx）；签名错误由调用方返回 4xx。
func (s *StripePaymentSrv) HandleWebhook(ctx context.Context, payload []byte, sigHeader string) error {
	whSecret := stripeWebhookSecret()
	if whSecret == "" {
		return ErrStripeWebhookNotConfigured
	}

	// 校验签名：强保证请求确来自 Stripe 且未被篡改，未验签的 webhook 可被伪造。
	event, err := webhook.ConstructEvent(payload, sigHeader, whSecret)
	if err != nil {
		return errors.Join(ErrStripeSignature, err)
	}

	if event.Type != "checkout.session.completed" {
		return nil // 其它事件忽略，正常 200
	}

	// 显式去重：以 event.ID 抢占一次性占位。订单状态守卫(WaitPay)是兜底，但
	// 在「已结算 / 正在结算」窗口内同一事件重投仍会做无谓的 DB 事务甚至触发误判，
	// 这里在入口处直接拦掉重复事件，更稳更省。Redis 不可用时退化为放行，由下游守卫兜底。
	if first, derr := s.claimStripeEvent(ctx, event.ID); derr != nil {
		log.LogrusObj.Warnf("stripe webhook dedupe redis err=%v event=%s, fallback to settle", derr, event.ID)
	} else if !first {
		log.LogrusObj.Infof("stripe webhook skip: event=%s already processed", event.ID)
		return nil
	}

	var sess stripe.CheckoutSession
	if err := json.Unmarshal(event.Data.Raw, &sess); err != nil {
		log.LogrusObj.Errorf("stripe webhook unmarshal session failed: %v", err)
		return err
	}
	if sess.PaymentStatus != stripe.CheckoutSessionPaymentStatusPaid {
		return nil // 会话完成但未支付，忽略
	}

	orderID, err := strconv.ParseUint(sess.ClientReferenceID, 10, 64)
	if err != nil || orderID == 0 {
		return errors.New("stripe webhook 缺少有效 client_reference_id")
	}
	var userID uint64
	if v, ok := sess.Metadata["user_id"]; ok {
		userID, _ = strconv.ParseUint(v, 10, 64)
	}
	if userID == 0 {
		return errors.New("stripe webhook 缺少 user_id")
	}

	if err := s.settleStripeOrder(ctx, uint(orderID), uint(userID), sess.ID, sess.AmountTotal, string(sess.Currency)); err != nil {
		// 确定性金额/币种不符已在事务内落 payment_anomaly 并返回成功；能走到这里的是 DB 等
		// 瞬时错误，释放去重键让 Stripe 重投再次进入，避免异常记录/正常清算被永久挡住。
		s.releaseStripeEvent(ctx, event.ID)
		return err
	}
	return nil
}

// claimStripeEvent 用 SETNX 对 event.ID 占位：true 表示首次处理。Redis 不可用时返回 err，
// 调用方据此降级放行（仍有订单状态守卫兜底），避免 Redis 抖动导致正常支付无法结算。
func (s *StripePaymentSrv) claimStripeEvent(ctx context.Context, eventID string) (bool, error) {
	if eventID == "" {
		return true, nil
	}
	if cache.RedisClient == nil {
		return true, nil
	}
	ok, err := cache.RedisClient.SetNX(ctx, stripeEventDedupeKey(eventID), "1", stripeEventDedupeTTL).Result()
	if err != nil {
		return false, err
	}
	return ok, nil
}

// releaseStripeEvent 删除去重占位键，使同一事件的后续重投能再次进入结算。
// 仅在瞬时失败时调用；删除失败只记日志（最坏结果是该事件需等占位键过期后才能重投）。
func (s *StripePaymentSrv) releaseStripeEvent(ctx context.Context, eventID string) {
	if eventID == "" || cache.RedisClient == nil {
		return
	}
	if err := cache.RedisClient.Del(ctx, stripeEventDedupeKey(eventID)).Err(); err != nil {
		log.LogrusObj.Warnf("stripe webhook release dedupe key failed event=%s err=%v", eventID, err)
	}
}

// settleStripeOrder 清算一笔 Stripe 已支付订单：外部已收资金转入商户托管，随后标记订单已付。
// 与钱包路径的区别：买家资金来自外部卡组织、不扣内部钱包，故 debit 记在外部清算账户。
// 幂等：订单 WaitPay 守卫为主，(order_id, direction) 唯一索引兜底并发重投。
// paidAmount/paidCurrency 取自 Stripe 会话(sess.AmountTotal/sess.Currency)，结算前需与
// 订单应付口径逐项核对，杜绝被篡改的 webhook 或币种漂移导致少收 / 错收。
func (s *StripePaymentSrv) settleStripeOrder(ctx context.Context, orderID, userID uint, providerRef string, paidAmount int64, paidCurrency string) error {
	var (
		paidProductID    uint
		paidNum          int
		paymentConfirmed bool
	)
	expectCurrency := stripeCurrency()

	err := orderpkg.NewOrderDao(ctx).Transaction(func(tx *gorm.DB) error {
		order, err := orderpkg.NewOrderDaoByDB(tx).GetOrderById(orderID, userID)
		if err != nil {
			log.LogrusObj.Error(err)
			return err
		}
		// 已被其它通道 / 上一次 webhook 结算过：幂等返回成功，不重复入账。
		if order.Type != consts.OrderWaitPay {
			matched, recordErr := clearing.RecordExternalDuplicateTx(
				tx, order, clearing.ChannelStripe, providerRef, strconv.FormatInt(paidAmount, 10), paidCurrency,
			)
			if recordErr != nil {
				return recordErr
			}
			if !matched {
				log.LogrusObj.Errorf("stripe duplicate payment recorded for refund review: order=%d session=%s", orderID, providerRef)
			}
			return nil
		}

		paidProductID = order.ProductID
		paidNum = order.Num

		// 实付口径统一走 orderPayableCents（命中促销取折后 FinalCents），与钱包 / Web3 路径一致。
		payable := orderPayableCents(order)

		// 结算前断言会话实付金额 / 币种与订单应付完全一致：签名只证明「来自 Stripe」，
		// 不保证「金额对得上订单」。币种大小写无关比较（Stripe 回传小写，建会话也用小写）。
		if paidAmount != payable || !strings.EqualFold(paidCurrency, expectCurrency) {
			log.LogrusObj.Errorf(
				"stripe settle reject order=%d amount/currency mismatch: paid=%d/%s expect=%d/%s",
				orderID, paidAmount, paidCurrency, payable, expectCurrency)
			// Stripe 已经确认实收，不能只返回错误再让入口去重键吞掉重投；持久化为待审核异常。
			if err := clearing.RecordExternalAnomalyTx(
				tx, order, clearing.ChannelStripe, providerRef, strconv.FormatInt(paidAmount, 10), paidCurrency,
				clearing.AnomalyReasonAmountMismatch,
			); err != nil {
				return err
			}
			return nil
		}

		// 清算：外部清算账户 debit、商户托管账户 credit。卖家要等订单完成后才真正入账。
		if err = clearing.RecordClearedTx(tx, order, clearing.ChannelStripe, providerRef, paidCurrency, nil); err != nil {
			log.LogrusObj.Error(err)
			return err
		}

		// 资金已进入托管，余下"扣库存 → 标记已付 → 商品归属转移 → outbox order.paid"走共享尾段。
		if err := finishPaymentConfirmationTx(tx, order); err != nil {
			return err
		}
		paymentConfirmed = true
		return nil
	})
	if err != nil {
		log.LogrusObj.Errorf("stripe settle order=%d failed: %v", orderID, err)
		return err
	}

	// 只有正常清算并推进订单后才核销预留。payment_anomaly 只是接管外部异常，订单仍是 WaitPay，
	// 绝不能把它的 reserved 提交掉，否则 DB 库存未扣而缓存预占消失，会重新暴露可售量。
	if paymentConfirmed {
		commitReservationBestEffort(ctx, paidProductID, paidNum)
	}
	return nil
}
