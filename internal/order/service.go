package order

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	conf "github.com/RedInn7/gomall/config"
	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/internal/address"
	"github.com/RedInn7/gomall/internal/coupon"
	"github.com/RedInn7/gomall/internal/product"
	"github.com/RedInn7/gomall/internal/promo"
	"github.com/RedInn7/gomall/internal/shared/dbmodel"
	"github.com/RedInn7/gomall/internal/shared/outbox"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/pkg/utils/snowflake"
	"github.com/RedInn7/gomall/repository/cache"
	"github.com/RedInn7/gomall/repository/db/dao"
	"github.com/RedInn7/gomall/repository/rabbitmq"
	"github.com/RedInn7/gomall/service/events"
)

const OrderTimeKey = "OrderTime"

// maxUnpaidOrdersPerUser 单个用户同时挂着的未支付订单上限：每张未支付单都占着库存预占，
// 不设上限就能靠下单不付款把库存占光。
// ponytail: 查数和建单不在一个事务里，并发下单可能略微超出；防刷够用，要精确再加用户级锁。
const maxUnpaidOrdersPerUser = 20

// ErrTooManyUnpaidOrders 未支付订单数到了上限。
var ErrTooManyUnpaidOrders = errors.New("未支付订单过多，请先支付或取消已有订单")

var OrderSrvIns *OrderSrv
var OrderSrvOnce sync.Once

// PromoCalculator 满减引擎在下单链路暴露的能力面。
// 以接口注入而非直引 *promo.PromoSrv，便于在测试中用替身验证降级与预算耗尽分支。
// CalculateBestDiscount 返回的 Candidates 是事务内预算挤光时的降级顺序。
type PromoCalculator interface {
	CalculateBestDiscount(ctx context.Context, items []promo.CartItem) (*promo.PromoApplyResp, error)
	ApplyDiscountInTx(tx *gorm.DB, orderID, ruleID uint, discountCents int64) error
}

type OrderSrv struct {
	promo PromoCalculator
}

// 装配约定（与其余领域一致）：
//   - NewOrderSrv 是唯一的依赖注入缝，仅供测试按需替换 promo 计算器；
//   - GetOrderSrv 是生产侧的默认装配，handler 一律走它，拿到全局单例。
//
// 二者不是新旧两套实现，而是同一套的「测试入口 / 生产入口」分工：
// NewOrderSrv 决定怎么装，GetOrderSrv 决定装好后给谁用。新增依赖时只改 NewOrderSrv。

// NewOrderSrv 构造下单服务，注入满减计算依赖。测试用替身、生产用真实 promo 服务。
func NewOrderSrv(promoCalc PromoCalculator) *OrderSrv {
	return &OrderSrv{promo: promoCalc}
}

// GetOrderSrv 返回生产默认装配的下单服务单例：满减依赖指向真实 promo 服务。
// handler 调用入口固定走这里，不直接 NewOrderSrv，保证全局一份装配。
func GetOrderSrv() *OrderSrv {
	OrderSrvOnce.Do(func() {
		OrderSrvIns = NewOrderSrv(promo.GetPromoSrv())
	})
	return OrderSrvIns
}

func (s *OrderSrv) OrderCreate(ctx context.Context, req *OrderCreateReq) (*Order, error) {
	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		util.LogrusObj.Error(err)
		return nil, err
	}
	if err = ensureUnpaidQuota(ctx, u.Id); err != nil {
		return nil, err
	}

	// 收货地址必须属于当前登录用户：address_id 同样是客户端可篡改字段，信它等于允许
	// 用别人的地址 id 下单（把货寄到他人地址 / 撞他人隐私）。
	if err = address.NewAddressDao(ctx).EnsureOwned(req.AddressID, u.Id); err != nil {
		util.LogrusObj.Errorf("address ownership check failed addr=%d user=%d err=%v", req.AddressID, u.Id, err)
		return nil, err
	}

	// 单价与卖家不属于创建订单的请求参数，一律根据 product_id 从商品表读取。
	// 一次反查同时拿到单价、类目和卖家；商品不存在或定价非法时直接拒单。
	unitCents, categoryID, bossID, err := resolveProductPricing(ctx, req.ProductID)
	if err != nil {
		util.LogrusObj.Errorf("resolve product pricing failed product=%d err=%v", req.ProductID, err)
		return nil, err
	}
	qty := int64(req.Num)

	// 满减与券都先于事务算好：纯读路径，DB 慢一点也不影响事务保持时间。
	// CategoryID 已由上面的权威反查一并取出，引擎无需再查库。
	quote, err := s.quoteOrder(ctx, u.Id, req.ProductID, categoryID, unitCents, qty, req.UserCouponID)
	if err != nil {
		return nil, err
	}

	order := &Order{
		UserID:    u.Id,
		ProductID: req.ProductID,
		BossID:    bossID,
		Num:       int(req.Num),
		Money:     unitCents, // 单价口径不变；减免结果记在 PromoDiscountCents / CouponDiscountCents / FinalCents
		Type:      consts.OrderWaitPay,
		AddressID: req.AddressID,
		OrderNum:  uint64(snowflake.GenSnowflakeID()),
		// 减免字段在事务内按扣预算 / 核销券的实际结果写回
		FinalCents: quote.subtotal,
	}

	// 1) Redis 预扣库存: available -> reserved，失败直接拒单
	if err = product.ReserveStock(ctx, req.ProductID, int64(req.Num)); err != nil {
		util.LogrusObj.Errorf("reserve stock failed product=%d num=%d err=%v", req.ProductID, req.Num, err)
		return nil, err
	}

	// 2) 同事务写订单 + 扣满减预算 + 核销券 + 写 outbox 事件
	//    满减 budget 耗尽降级：宁可少给折扣也不能让用户下不了单。
	err = dao.NewDBClient(ctx).Transaction(func(tx *gorm.DB) error {
		if e := NewOrderDaoByDB(tx).CreateOrder(order); e != nil {
			return e
		}
		if e := s.applyDiscountsInTx(tx, order, quote); e != nil {
			return e
		}

		return outbox.NewOutboxDaoByDB(tx).Insert(
			"order", "OrderCreated", "order.created", order.ID,
			events.OrderCreated{
				OrderID:   order.ID,
				OrderNum:  order.OrderNum,
				UserID:    u.Id,
				ProductID: req.ProductID,
				Num:       int(req.Num),
			},
		)
	})
	if err != nil {
		util.LogrusObj.Error(err)
		// 3) 事务失败：释放刚扣的预占库存，回到 available
		if relErr := cache.ReleaseReservation(ctx, req.ProductID, int64(req.Num)); relErr != nil {
			util.LogrusObj.Errorf("release reservation on tx failure failed: %v", relErr)
		}
		return nil, err
	}

	data := redis.Z{
		Score:  float64(time.Now().Unix()) + 15*time.Minute.Seconds(),
		Member: order.OrderNum,
	}
	cache.RedisClient.ZAdd(cache.RedisContext, OrderTimeKey, data)

	if rabbitmq.GlobalRabbitMQ != nil {
		if pubErr := rabbitmq.PublishOrderCancelDelay(ctx, order.OrderNum, rabbitmq.OrderCancelDelay); pubErr != nil {
			util.LogrusObj.Errorf("publish delay cancel failed orderNum=%d err=%v", order.OrderNum, pubErr)
		}
	}

	return order, nil
}

func (s *OrderSrv) OrderList(ctx context.Context, req *OrderListReq) (*OrderListResp, error) {
	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		util.LogrusObj.Error(err)
		return nil, err
	}
	orders, err := NewOrderDao(ctx).ListOrderByCondition(u.Id, req)
	if err != nil {
		util.LogrusObj.Error(err)
		return nil, err
	}
	for i := range orders.List {
		if conf.Config.System.UploadModel == consts.UploadModelLocal {
			orders.List[i].ImgPath = conf.Config.PhotoPath.PhotoHost + conf.Config.System.HttpPort + conf.Config.PhotoPath.ProductPath + orders.List[i].ImgPath
		}
	}

	return orders, nil
}

func (s *OrderSrv) OrderShow(ctx context.Context, req *OrderShowReq) (*OrderListRespItem, error) {
	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		util.LogrusObj.Error(err)
		return nil, err
	}
	order, err := NewOrderDao(ctx).ShowOrderById(req.OrderId, u.Id)
	if err != nil {
		util.LogrusObj.Error(err)
		return nil, err
	}
	if order == nil || order.ID == 0 {
		return nil, errors.New("没找到数据")
	}

	if conf.Config.System.UploadModel == consts.UploadModelLocal {
		order.ImgPath = conf.Config.PhotoPath.PhotoHost + conf.Config.System.HttpPort + conf.Config.PhotoPath.ProductPath + order.ImgPath
	}

	return order, nil
}

// orderQuote 事务外算好的优惠方案（纯读），事务内按它扣预算、核销券。
type orderQuote struct {
	subtotal int64
	promos   []promo.PromoCandidate // 按减免从大到小；前一条预算被并发挤光就试下一条
	coupon   *coupon.UserCoupon     // nil = 不用券
	batch    *coupon.CouponBatch
}

// quoteOrder 选满减候选、校验要用的券。满减试算失败降级成无折扣，不阻断下单；
// 券是用户主动选的，不可用 / 不满门槛就拒单，不能悄悄按原价收钱。
func (s *OrderSrv) quoteOrder(ctx context.Context, userID, productID uint, categoryID, unitCents, qty int64, userCouponID uint) (*orderQuote, error) {
	q := &orderQuote{subtotal: unitCents * qty}
	if s.promo != nil {
		apply, err := s.promo.CalculateBestDiscount(ctx, buildPromoCartItems(productID, categoryID, unitCents, qty))
		if err != nil {
			// 失败降级：CalculateBestDiscount 不应阻断下单（SLO 承诺：不影响 happy path）。
			util.LogrusObj.Warnf("[promo] calculate best discount failed product=%d err=%v, fallback to no-discount",
				productID, err)
		} else {
			q.promos = promoCandidates(apply)
		}
	}
	if userCouponID == 0 {
		return q, nil
	}
	uc, batch, err := coupon.NewCouponDao(ctx).GetUsable(userID, userCouponID, time.Now())
	if err != nil {
		return nil, err
	}
	base := q.subtotal
	if len(q.promos) > 0 {
		base -= q.promos[0].DiscountCents
	}
	// 事务里满减只可能降级（减得更少），券的基数只会变大，这里按最优满减判门槛是最严的口径。
	if coupon.ComputeDiscount(batch, base) <= 0 {
		return nil, coupon.ErrCouponThresholdNotMet
	}
	q.coupon, q.batch = uc, batch
	return q, nil
}

// promoCandidates 兼容只填了 RuleID 没带 Candidates 的计算器实现。
func promoCandidates(r *promo.PromoApplyResp) []promo.PromoCandidate {
	if r == nil || r.RuleID == 0 || r.DiscountCents <= 0 {
		return nil
	}
	if len(r.Candidates) > 0 {
		return r.Candidates
	}
	return []promo.PromoCandidate{{Rule: &promo.PromoRule{Model: dbmodel.Model{ID: r.RuleID}}, DiscountCents: r.DiscountCents}}
}

// applyDiscountsInTx 在下单事务里落实优惠：先满减、后券。
//   - 满减按候选顺序扣预算，被并发挤光就降级到次优，全挤光才无折扣；
//   - 券在满减后的金额上算，条件 UPDATE 核销，同一张券被两单并发选中时后到的整单回滚；
//   - 订单上的减免字段与 FinalCents 一次写回，同进同退。
func (s *OrderSrv) applyDiscountsInTx(tx *gorm.DB, o *Order, q *orderQuote) error {
	for _, c := range q.promos {
		err := s.promo.ApplyDiscountInTx(tx, o.ID, c.Rule.ID, c.DiscountCents)
		if err == nil {
			o.PromoRuleID, o.PromoDiscountCents = c.Rule.ID, c.DiscountCents
			break
		}
		if !errors.Is(err, promo.ErrPromoBudgetExhausted) {
			return err
		}
		util.LogrusObj.Warnf("[promo] downgrade rule=%d budget exhausted, order=%d tries next candidate",
			c.Rule.ID, o.ID)
	}

	base := q.subtotal - o.PromoDiscountCents
	if q.coupon != nil {
		d := coupon.ComputeDiscount(q.batch, base)
		if d <= 0 {
			return coupon.ErrCouponThresholdNotMet
		}
		if err := coupon.NewCouponDaoByDB(tx).UseInTx(q.coupon.ID, o.UserID, o.ID, time.Now()); err != nil {
			return err
		}
		o.UserCouponID, o.CouponDiscountCents = q.coupon.ID, d
	}
	o.FinalCents = base - o.CouponDiscountCents
	if o.FinalCents < 0 {
		// 建规则 / 建批次都校验了金额，这里只是兜底，不让实付变负数
		util.LogrusObj.Warnf("[promo] discounts exceed subtotal order=%d, clamp final to 0", o.ID)
		o.FinalCents = 0
	}
	if o.PromoRuleID == 0 && o.UserCouponID == 0 {
		return nil // 建单时 FinalCents 已是原价
	}
	util.LogrusObj.Infof("[promo] order=%d rule=%d promo=%d coupon=%d couponDiscount=%d final=%d",
		o.ID, o.PromoRuleID, o.PromoDiscountCents, o.UserCouponID, o.CouponDiscountCents, o.FinalCents)
	return NewOrderDaoByDB(tx).UpdateDiscountFields(o)
}

// buildPromoCartItems 给满减引擎拼一行 CartItem。单价与类目均由调用方从权威反查传入，
// 这里只做纯拼装，不再触库——避免与 resolveProductPricing 重复查询同一商品。
func buildPromoCartItems(productID uint, categoryID, unitCents, qty int64) []promo.CartItem {
	return []promo.CartItem{{
		ProductID:  int64(productID),
		CategoryID: categoryID,
		UnitCents:  unitCents,
		Quantity:   qty,
	}}
}

// resolveProductPricing 从商品表反查权威单价（分）、类目与卖家，作为下单计费 / 打款的唯一来源。
// 单价换算与取价优先级（DiscountPrice→Price）集中在 product.Product.UnitCents；商品不存在、已下架或
// 定价非法直接返回 error 由调用方拒单，绝不回退到客户端传入的金额 / 卖家。
func resolveProductPricing(ctx context.Context, productID uint) (unitCents int64, categoryID int64, bossID uint, err error) {
	pdao := product.NewProductDao(ctx)
	if pdao == nil || pdao.DB == nil {
		return 0, 0, 0, errors.New("product dao 不可用，无法核定单价")
	}
	p, perr := pdao.GetProductById(productID)
	if perr != nil || p == nil {
		return 0, 0, 0, fmt.Errorf("商品不存在或查询失败 product=%d: %w", productID, perr)
	}
	if !p.OnSale {
		return 0, 0, 0, product.ErrProductNotOnSale
	}
	cents, ok := p.UnitCents()
	if !ok {
		return 0, 0, 0, fmt.Errorf("商品定价非法 product=%d discount=%q price=%q", productID, p.DiscountPrice, p.Price)
	}
	return cents, int64(p.CategoryID), p.BossID, nil
}

// ensureUnpaidQuota 未支付订单数到上限就拒绝再下单（同步 / 异步下单共用）。
func ensureUnpaidQuota(ctx context.Context, userID uint) error {
	n, err := NewOrderDao(ctx).CountUnpaidByUser(userID)
	if err != nil {
		util.LogrusObj.Errorf("count unpaid orders failed user=%d err=%v", userID, err)
		return err
	}
	if n >= maxUnpaidOrdersPerUser {
		return ErrTooManyUnpaidOrders
	}
	return nil
}

// OrderDelete 删除订单（物理删除）。成功时不回传订单体（保持既有 API 契约：data 为 null）。
// 只能删资金已闭环的终态订单，判定见 DeleteOrderById。
func (s *OrderSrv) OrderDelete(ctx context.Context, req *OrderDeleteReq) (*OrderListRespItem, error) {
	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		util.LogrusObj.Error(err)
		return nil, err
	}
	db := NewOrderDao(ctx)
	if _, err = db.GetOrderById(req.OrderId, u.Id); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("没有查找到数据")
		}
		util.LogrusObj.Error(err)
		return nil, err
	}

	deleted, err := db.DeleteOrderById(req.OrderId, u.Id)
	if err != nil {
		util.LogrusObj.Error(err)
		return nil, err
	}
	if !deleted {
		return nil, errors.New("订单尚未结束或资金未结清，暂不能删除")
	}

	return nil, nil
}
