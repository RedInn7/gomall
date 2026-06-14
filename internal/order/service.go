package order

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	conf "github.com/RedInn7/gomall/config"
	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/internal/product"
	"github.com/RedInn7/gomall/internal/promo"
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

var OrderSrvIns *OrderSrv
var OrderSrvOnce sync.Once

// PromoCalculator 满减引擎在下单链路暴露的能力面。
// 以接口注入而非直引 *promo.PromoSrv，便于在测试中用替身验证降级与预算耗尽分支。
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

	// 单价以服务端商品表为准，不取 req.Money：金额是安全敏感字段，客户端可任意改写，
	// 信它等于把定价权交给买家（money=1 即可 1 分钱下单）。这里一次反查同时拿到单价与
	// 类目；商品不存在或定价非法直接拒单，绝不回退到 req.Money（回退即重新打开漏洞）。
	unitCents, categoryID, err := resolveProductPricing(ctx, req.ProductID)
	if err != nil {
		util.LogrusObj.Errorf("resolve product pricing failed product=%d err=%v", req.ProductID, err)
		return nil, err
	}
	qty := int64(req.Num)
	subtotalCents := unitCents * qty

	// 满减计算先于事务发生：纯读路径，DB 慢一点也不影响事务保持时间。
	// CategoryID 已由上面的权威反查一并取出，引擎无需再查库。
	cartItems := buildPromoCartItems(req.ProductID, categoryID, unitCents, qty)
	promoApply, promoErr := s.promo.CalculateBestDiscount(ctx, cartItems)
	if promoErr != nil {
		// 失败降级：CalculateBestDiscount 不应阻断下单（SLO 承诺：不影响 happy path）。
		util.LogrusObj.Warnf("[promo] calculate best discount failed product=%d err=%v, fallback to no-discount",
			req.ProductID, promoErr)
		promoApply = &promo.PromoApplyResp{OriginalCents: subtotalCents, FinalCents: subtotalCents}
	}

	discountCents, ruleID := promoApply.DiscountCents, promoApply.RuleID
	finalCents := subtotalCents - discountCents
	if finalCents < 0 {
		// 规则配置畸形（discount > subtotal）兜底到 0，不让用户实付变负数
		util.LogrusObj.Warnf("[promo] discount %d > subtotal %d rule=%d, clamp final to 0",
			discountCents, subtotalCents, ruleID)
		finalCents = 0
	}

	order := &Order{
		UserID:    u.Id,
		ProductID: req.ProductID,
		BossID:    req.BossID,
		Num:       int(req.Num),
		Money:     unitCents, // 单价口径不变；满减结果记在 PromoDiscountCents / FinalCents
		Type:      consts.OrderWaitPay,
		AddressID: req.AddressID,
		OrderNum:  uint64(snowflake.GenSnowflakeID()),
		// 满减字段会在事务内根据 ApplyDiscountInTx 的结果再次确认 / 降级写回
		PromoRuleID:        ruleID,
		PromoDiscountCents: discountCents,
		FinalCents:         finalCents,
	}

	// 1) Redis 预扣库存: available -> reserved，失败直接拒单
	if err = cache.ReserveStock(ctx, req.ProductID, int64(req.Num)); err != nil {
		util.LogrusObj.Errorf("reserve stock failed product=%d num=%d err=%v", req.ProductID, req.Num, err)
		return nil, err
	}

	// 2) 同事务写订单 + 应用满减 + 写 outbox 事件
	//    满减 budget 耗尽降级：宁可不给折扣也不能让用户下不了单。
	err = dao.NewDBClient(ctx).Transaction(func(tx *gorm.DB) error {
		if e := NewOrderDaoByDB(tx).CreateOrder(order); e != nil {
			return e
		}

		// 试着扣预算；budget 用尽则降级为无折扣，并改写订单上的满减字段
		if order.PromoRuleID != 0 && order.PromoDiscountCents > 0 {
			applyErr := s.promo.ApplyDiscountInTx(tx, order.ID, order.PromoRuleID, order.PromoDiscountCents)
			if applyErr != nil {
				if errors.Is(applyErr, promo.ErrPromoBudgetExhausted) {
					util.LogrusObj.Warnf("[promo] downgrade rule=%d budget exhausted, order=%d falls back to no-discount",
						order.PromoRuleID, order.ID)
					order.PromoRuleID = 0
					order.PromoDiscountCents = 0
					order.FinalCents = subtotalCents
					if uerr := NewOrderDaoByDB(tx).UpdatePromoFields(order.ID,
						order.PromoRuleID, order.PromoDiscountCents, order.FinalCents); uerr != nil {
						return uerr
					}
				} else {
					return applyErr
				}
			} else {
				util.LogrusObj.Infof("[promo] applied rule=%d discount=%d order=%d final=%d",
					order.PromoRuleID, order.PromoDiscountCents, order.ID, order.FinalCents)
			}
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

func (s *OrderSrv) OrderListOld(ctx context.Context, req *OrderListReq) (*OrderListResp, error) {
	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		util.LogrusObj.Error(err)
		return nil, err
	}
	orders, _, err := NewOrderDao(ctx).ListOrderByConditionOld(u.Id, req)
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

// resolveProductPricing 从商品表反查权威单价（分）与类目，作为下单计费的唯一价格来源。
// 取价优先级：DiscountPrice（用户实付价）→ Price（原价兜底）。任一步失败都返回 error，
// 由调用方拒单；绝不静默回退到客户端传入的金额，否则等于把篡改后的价格重新放行。
func resolveProductPricing(ctx context.Context, productID uint) (unitCents int64, categoryID int64, err error) {
	pdao := product.NewProductDao(ctx)
	if pdao == nil || pdao.DB == nil {
		return 0, 0, errors.New("product dao 不可用，无法核定单价")
	}
	p, perr := pdao.GetProductById(productID)
	if perr != nil || p == nil {
		return 0, 0, fmt.Errorf("商品不存在或查询失败 product=%d: %w", productID, perr)
	}
	cents, ok := yuanToCents(p.DiscountPrice)
	if !ok || cents <= 0 {
		cents, ok = yuanToCents(p.Price)
	}
	if !ok || cents <= 0 {
		return 0, 0, fmt.Errorf("商品定价非法 product=%d discount=%q price=%q", productID, p.DiscountPrice, p.Price)
	}
	return cents, int64(p.CategoryID), nil
}

// resolveUnitCents 仅取权威单价（分），异步消费侧不需要类目时用它。
func resolveUnitCents(ctx context.Context, productID uint) (int64, error) {
	cents, _, err := resolveProductPricing(ctx, productID)
	return cents, err
}

// yuanToCents 把「元」字符串（如 "99.50"）转成「分」。商品价以两位小数的元为单位存储，
// 与订单的分口径不同，这里统一换算。解析失败或为负返回 ok=false，交由上层兜底/拒单。
func yuanToCents(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	yuan, err := strconv.ParseFloat(s, 64)
	if err != nil || yuan < 0 {
		return 0, false
	}
	// Round 抵消二进制浮点误差（99.50*100 可能算出 9949.999...）。
	return int64(math.Round(yuan * 100)), true
}

// OrderDelete 软删订单。成功时不回传订单体（保持既有 API 契约：data 为 null），
// 仅用 ShowOrderById 做存在性校验。
func (s *OrderSrv) OrderDelete(ctx context.Context, req *OrderDeleteReq) (*OrderListRespItem, error) {
	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		util.LogrusObj.Error(err)
		return nil, err
	}
	db := NewOrderDao(ctx)
	ret, err := db.ShowOrderById(req.OrderId, u.Id)
	if err != nil {
		util.LogrusObj.Error("ShowOrderById失败，err:", err)
		return nil, err
	}
	if ret == nil || ret.ID == 0 {
		return nil, errors.New("没有查找到数据")
	}

	if err = db.DeleteOrderById(req.OrderId, u.Id); err != nil {
		util.LogrusObj.Error(err)
		return nil, err
	}

	return nil, nil
}
