package service

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	conf "github.com/RedInn7/gomall/config"
	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/internal/product"
	"github.com/RedInn7/gomall/internal/promo"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/pkg/utils/snowflake"
	"github.com/RedInn7/gomall/repository/cache"
	"github.com/RedInn7/gomall/repository/db/dao"
	"github.com/RedInn7/gomall/repository/db/model"
	"github.com/RedInn7/gomall/repository/rabbitmq"
	"github.com/RedInn7/gomall/service/events"
	"github.com/RedInn7/gomall/types"
)

const OrderTimeKey = "OrderTime"

var OrderSrvIns *OrderSrv
var OrderSrvOnce sync.Once

type OrderSrv struct {
}

func GetOrderSrv() *OrderSrv {
	OrderSrvOnce.Do(func() {
		OrderSrvIns = &OrderSrv{}
	})
	return OrderSrvIns
}

func (s *OrderSrv) OrderCreate(ctx context.Context, req *types.OrderCreateReq) (resp interface{}, err error) {
	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		util.LogrusObj.Error(err)
		return nil, err
	}

	unitCents := int64(req.Money)
	qty := int64(req.Num)
	subtotalCents := unitCents * qty

	// 满减计算先于事务发生：纯读路径，DB 慢一点也不影响事务保持时间。
	// 走 Product DAO 反查 CategoryID —— 老客户端不传类目信息，由服务端兜底。
	cartItems := buildPromoCartItems(ctx, req.ProductID, unitCents, qty)
	promoApply, promoErr := promo.GetPromoSrv().CalculateBestDiscount(ctx, cartItems)
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

	order := &model.Order{
		UserID:    u.Id,
		ProductID: req.ProductID,
		BossID:    req.BossID,
		Num:       int(req.Num),
		Money:     unitCents, // 单价口径不变；满减结果记在 PromoDiscountCents / FinalCents
		Type:      consts.UnPaid,
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
		if e := dao.NewOrderDaoByDB(tx).CreateOrder(order); e != nil {
			return e
		}

		// 试着扣预算；budget 用尽则降级为无折扣，并改写订单上的满减字段
		if order.PromoRuleID != 0 && order.PromoDiscountCents > 0 {
			applyErr := promo.GetPromoSrv().ApplyDiscountInTx(tx, order.ID, order.PromoRuleID, order.PromoDiscountCents)
			if applyErr != nil {
				if errors.Is(applyErr, promo.ErrPromoBudgetExhausted) {
					util.LogrusObj.Warnf("[promo] downgrade rule=%d budget exhausted, order=%d falls back to no-discount",
						order.PromoRuleID, order.ID)
					order.PromoRuleID = 0
					order.PromoDiscountCents = 0
					order.FinalCents = subtotalCents
					if uerr := dao.NewOrderDaoByDB(tx).UpdatePromoFields(order.ID,
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

		return dao.NewOutboxDaoByDB(tx).Insert(
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
		return
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

	resp = order
	return
}

func (s *OrderSrv) OrderList(ctx context.Context, req *types.OrderListReq) (resp interface{}, err error) {
	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		util.LogrusObj.Error(err)
		return nil, err
	}
	orders, err := dao.NewOrderDao(ctx).ListOrderByCondition(u.Id, req)
	if err != nil {
		util.LogrusObj.Error(err)
		return
	}
	for i := range orders.List {
		if conf.Config.System.UploadModel == consts.UploadModelLocal {
			orders.List[i].ImgPath = conf.Config.PhotoPath.PhotoHost + conf.Config.System.HttpPort + conf.Config.PhotoPath.ProductPath + orders.List[i].ImgPath
		}
	}

	return orders, nil
}

func (s *OrderSrv) OrderListOld(ctx context.Context, req *types.OrderListReq) (resp interface{}, err error) {
	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		util.LogrusObj.Error(err)
		return nil, err
	}
	orders, _, err := dao.NewOrderDao(ctx).ListOrderByConditionOld(u.Id, req)
	if err != nil {
		util.LogrusObj.Error(err)
		return
	}
	for i := range orders.List {
		if conf.Config.System.UploadModel == consts.UploadModelLocal {
			orders.List[i].ImgPath = conf.Config.PhotoPath.PhotoHost + conf.Config.System.HttpPort + conf.Config.PhotoPath.ProductPath + orders.List[i].ImgPath
		}
	}

	return orders, nil
}

func (s *OrderSrv) OrderShow(ctx context.Context, req *types.OrderShowReq) (resp interface{}, err error) {
	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		util.LogrusObj.Error(err)
		return nil, err
	}
	order, err := dao.NewOrderDao(ctx).ShowOrderById(req.OrderId, u.Id)
	if err != nil {
		util.LogrusObj.Error(err)
		return
	}
	if order == nil || order.ID == 0 {
		return nil, errors.New("没找到数据")
	}

	if conf.Config.System.UploadModel == consts.UploadModelLocal {
		order.ImgPath = conf.Config.PhotoPath.PhotoHost + conf.Config.System.HttpPort + conf.Config.PhotoPath.ProductPath + order.ImgPath
	}

	resp = order

	return
}

// buildPromoCartItems 给满减引擎拼一行 CartItem。
// 老客户端 OrderCreateReq 没有 CategoryID，服务端回查 Product 兜底；
// 商品不存在 / DB 失败时降级返回不带类目的 cartItem，引擎仅匹配商品级 / 全场规则。
func buildPromoCartItems(ctx context.Context, productID uint, unitCents, qty int64) []promo.CartItem {
	item := promo.CartItem{
		ProductID: int64(productID),
		UnitCents: unitCents,
		Quantity:  qty,
	}
	pdao := product.NewProductDao(ctx)
	if pdao != nil && pdao.DB != nil {
		if p, err := pdao.GetProductById(productID); err == nil && p != nil {
			item.CategoryID = int64(p.CategoryID)
		}
	}
	return []promo.CartItem{item}
}

func (s *OrderSrv) OrderDelete(ctx context.Context, req *types.OrderDeleteReq) (resp interface{}, err error) {
	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		util.LogrusObj.Error(err)
		return
	}
	db := dao.NewOrderDao(ctx)
	var ret *types.OrderListRespItem
	ret, err = db.ShowOrderById(req.OrderId, u.Id)
	if err != nil {
		util.LogrusObj.Error("ShowOrderById失败，err:", err)
		return nil, err
	}
	if ret == nil || ret.ID == 0 {
		return nil, errors.New("没有查找到数据")
	}

	err = db.DeleteOrderById(req.OrderId, u.Id)
	if err != nil {
		util.LogrusObj.Error(err)
		return
	}

	return
}
