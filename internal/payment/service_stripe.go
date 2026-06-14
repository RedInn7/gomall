package payment

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/stripe/stripe-go/v82"
	"github.com/stripe/stripe-go/v82/checkout/session"
	"github.com/stripe/stripe-go/v82/webhook"
	"gorm.io/gorm"

	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/internal/money"
	orderpkg "github.com/RedInn7/gomall/internal/order"
	"github.com/RedInn7/gomall/internal/product"
	"github.com/RedInn7/gomall/internal/shared/outbox"
	"github.com/RedInn7/gomall/internal/user"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
	"github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/cache"
	"github.com/RedInn7/gomall/service/events"
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

	// 实付口径与钱包路径对齐：命中促销(PromoRuleID!=0)即以折后实付 FinalCents 为准。
	// 不能用 FinalCents>0 判：满减全额抵扣到 0 时 FinalCents==0 是合法实付，用 >0 会误回退折前全价多扣。
	payable := order.Money * int64(order.Num)
	if order.PromoRuleID != 0 {
		payable = order.FinalCents
	}
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

	return s.settleStripeOrder(ctx, uint(orderID), uint(userID))
}

// settleStripeOrder 结算一笔 Stripe 已支付订单：标记已付 + 扣库存 + 商品归属转移 + 卖家入账 + 写台账 + outbox。
// 与钱包路径的区别：买家资金来自外部卡组织、不扣内部钱包，故 debit 记在平台清算账户。
// 幂等：订单 WaitPay 守卫为主，(order_id, direction) 唯一索引兜底并发重投。
func (s *StripePaymentSrv) settleStripeOrder(ctx context.Context, orderID, userID uint) error {
	var (
		paidProductID uint
		paidNum       int
	)

	err := orderpkg.NewOrderDao(ctx).Transaction(func(tx *gorm.DB) error {
		order, err := orderpkg.NewOrderDaoByDB(tx).GetOrderById(orderID, userID)
		if err != nil {
			log.LogrusObj.Error(err)
			return err
		}
		// 已被其它通道 / 上一次 webhook 结算过：幂等返回成功，不重复入账。
		if order.Type != consts.OrderWaitPay {
			log.LogrusObj.Infof("stripe settle skip: order=%d already settled (type=%d)", orderID, order.Type)
			return nil
		}

		productID := order.ProductID
		num := order.Num
		bossID := order.BossID
		paidProductID = productID
		paidNum = num

		// 实付口径与钱包路径对齐：命中促销以折后实付 FinalCents 为准（不用 FinalCents>0 判，防全额抵扣误回退）。
		payable := order.Money * int64(num)
		if order.PromoRuleID != 0 {
			payable = order.FinalCents
		}

		userDao := user.NewUserDaoByDB(tx)
		ledgerDao := money.NewLedgerDaoByDB(tx)

		// 卖家入账：解密服务端余额 + payable，再加密写回，同事务追加 credit 流水。
		boss, err := userDao.GetUserByIdForUpdate(bossID)
		if err != nil {
			log.LogrusObj.Error(err)
			return err
		}
		bossMoney, err := boss.DecryptMoney()
		if err != nil {
			log.LogrusObj.Error(err)
			return err
		}
		bossBalanceAfter := bossMoney + payable
		boss.Money = strconv.FormatInt(bossBalanceAfter, 10)
		if boss.Money, err = boss.EncryptMoney(); err != nil {
			log.LogrusObj.Error(err)
			return err
		}
		if err = userDao.UpdateUserById(bossID, boss); err != nil {
			log.LogrusObj.Error(err)
			return err
		}
		if err = ledgerDao.AppendTransaction(bossID, order.ID, money.DirectionCredit, payable, bossBalanceAfter, money.BizTypeStripePay); err != nil {
			log.LogrusObj.Error(err)
			return err
		}
		// 复式对手方：平台 Stripe 清算账户记 debit（外部资金入口），保持借贷平衡 + (order_id,debit) 幂等。
		if err = ledgerDao.AppendTransaction(money.StripeClearingUserID, order.ID, money.DirectionDebit, payable, 0, money.BizTypeStripePay); err != nil {
			log.LogrusObj.Error(err)
			return err
		}

		// 原子扣减库存（条件 UPDATE 防超卖）。
		prod, err := product.NewProductDaoByDB(tx).GetProductById(productID)
		if err != nil {
			log.LogrusObj.Error(err)
			return err
		}
		ok, err := product.NewProductDaoWithDB(tx).DeductStock(productID, num)
		if err != nil {
			log.LogrusObj.Error(err)
			return err
		}
		if !ok {
			return errors.New("存在超卖问题")
		}

		// 订单状态：WaitPay 守卫塞进 WHERE，杜绝重复支付。
		paidOK, err := orderpkg.NewOrderDaoByDB(tx).MarkOrderPaidWithCheck(order.ID, userID)
		if err != nil {
			log.LogrusObj.Error(err)
			return err
		}
		if !paidOK {
			return errors.New("订单状态已变更，无法重复支付")
		}

		// 商品归属转移给买家（与钱包路径一致）。
		buyer, err := userDao.GetUserById(userID)
		if err != nil {
			log.LogrusObj.Error(err)
			return err
		}
		productUser := product.Product{
			Name:          prod.Name,
			CategoryID:    prod.CategoryID,
			Title:         prod.Title,
			Info:          prod.Info,
			ImgPath:       prod.ImgPath,
			Price:         prod.Price,
			DiscountPrice: prod.DiscountPrice,
			Num:           num,
			OnSale:        false,
			BossID:        userID,
			BossName:      buyer.UserName,
			BossAvatar:    buyer.Avatar,
		}
		if err = product.NewProductDaoByDB(tx).CreateProduct(&productUser); err != nil {
			log.LogrusObj.Error(err)
			return err
		}

		// outbox：order.paid，投递交给 publisher 异步处理。
		return outbox.NewOutboxDaoByDB(tx).Insert(
			"order", "OrderPaid", "order.paid", order.ID,
			events.OrderPaid{
				OrderID:   order.ID,
				OrderNum:  order.OrderNum,
				UserID:    userID,
				ProductID: productID,
				Num:       num,
			},
		)
	})
	if err != nil {
		log.LogrusObj.Errorf("stripe settle order=%d failed: %v", orderID, err)
		return err
	}

	// TX 已把 product.Num 真正扣减；同步把 Redis reserved 桶减掉。
	if paidProductID > 0 && paidNum > 0 {
		if cErr := cache.CommitReservation(ctx, paidProductID, int64(paidNum)); cErr != nil {
			log.LogrusObj.Errorf("commit reservation failed product=%d num=%d err=%v", paidProductID, paidNum, cErr)
		}
	}
	return nil
}
