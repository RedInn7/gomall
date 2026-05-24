package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/pkg/e"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/pkg/utils/snowflake"
	"github.com/RedInn7/gomall/repository/cache"
	"github.com/RedInn7/gomall/repository/db/dao"
	"github.com/RedInn7/gomall/repository/db/model"
	"github.com/RedInn7/gomall/service/events"
	"github.com/RedInn7/gomall/types"
)

// PreorderSrv 预售两段式支付。
// 三个写动作（付定金 / 付尾款 / 取消）共享一组不变量：
//   1) 时间窗口校验在事务之外，业务码直接 4xx
//   2) 真正的余额扣减 + 库存动作 + 状态机推进 + outbox 写入在同一事务
//   3) 失败路径走 Saga：cache 操作放在事务外，事务失败时回滚 cache
//
// 业务承诺（README + 法律合规）：定金期外不退；尾款窗口期外按预售须知没收定金。
// 上述承诺在用户首次进入预售页时必须由前端展示，82xxx 业务码用于客服话术。
type PreorderSrv struct{}

var (
	preorderSrvIns  *PreorderSrv
	preorderSrvOnce sync.Once
)

func GetPreorderSrv() *PreorderSrv {
	preorderSrvOnce.Do(func() { preorderSrvIns = &PreorderSrv{} })
	return preorderSrvIns
}

// codedError 把业务码透出给上层 handler，handler 通过 errors.As 解出。
type codedError struct {
	Code int
	Msg  string
}

func (c *codedError) Error() string { return c.Msg }

func newCodedError(code int) error {
	return &codedError{Code: code, Msg: e.GetMsg(code)}
}

// CodeOf 提取 codedError 里的业务码；非 codedError 返回 e.ERROR。
func CodeOf(err error) int {
	if err == nil {
		return e.SUCCESS
	}
	var ce *codedError
	if errors.As(err, &ce) {
		return ce.Code
	}
	return e.ERROR
}

// 时钟函数指针，方便测试替换。生产路径恒为 time.Now。
var nowFn = time.Now

// PayDeposit 定金期内付定金：建预售订单 + 锁库存 + 扣定金 + 写 outbox。
//   - 时间窗校验：now ∈ [DepositStartAt, DepositEndAt)
//   - 复用 inventory Lua 的 reserve 桶：定金期占住库存但不真扣
//   - 失败路径：事务失败 → ReleaseReservation 回滚 reserved
func (s *PreorderSrv) PayDeposit(ctx context.Context, req *types.PreorderDepositReq) (*types.PreorderActionResp, error) {
	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		return nil, err
	}
	if len(req.Key) != consts.EncryptMoneyKeyLength {
		return nil, errors.New("支付密码长度错误")
	}

	// 1) 查预售配置 + 窗口校验
	pp, err := dao.NewPreorderDao(ctx).GetPreorderByProductID(req.ProductID)
	if err != nil {
		return nil, err
	}
	now := nowFn()
	if now.Before(pp.DepositStartAt) || !now.Before(pp.DepositEndAt) {
		return nil, newCodedError(e.ErrPreorderNotInDepositWindow)
	}

	// 2) Redis reserve（available -> reserved）；预售单数固定 1（业务约束：1 单 1 件）
	if err := cache.ReserveStock(ctx, req.ProductID, 1); err != nil {
		util.LogrusObj.Errorf("preorder reserve stock failed product=%d err=%v", req.ProductID, err)
		return nil, err
	}

	// 3) 同事务：建订单 + 扣定金 + Mark + outbox
	var (
		order      = &model.Order{}
		releaseErr error
	)
	err = dao.NewDBClient(ctx).Transaction(func(tx *gorm.DB) error {
		order.UserID = u.Id
		order.ProductID = req.ProductID
		order.BossID = req.BossID
		order.AddressID = req.AddressID
		order.Num = 1
		order.Money = pp.DepositCents + pp.FinalCents // 累计金额；分两次扣
		order.Type = consts.OrderWaitPay
		order.OrderNum = uint64(snowflake.GenSnowflakeID())

		if e := dao.NewOrderDaoByDB(tx).CreateOrder(order); e != nil {
			return e
		}

		if e := debitUser(tx, u.Id, req.BossID, req.Key, pp.DepositCents); e != nil {
			return e
		}

		paidAt := now
		ok, e := dao.NewPreorderDaoByDB(tx).MarkDepositPaid(tx, order.ID, paidAt)
		if e != nil {
			return e
		}
		if !ok {
			return errors.New("preorder stage 推进失败：订单状态已变更")
		}

		return dao.NewOutboxDaoByDB(tx).Insert(
			"order", "PreorderDepositPaid", "preorder.deposit.paid", order.ID,
			events.PreorderDepositPaid{
				OrderID:   order.ID,
				OrderNum:  order.OrderNum,
				UserID:    u.Id,
				ProductID: req.ProductID,
				Deposit:   pp.DepositCents,
			},
		)
	})
	if err != nil {
		if relErr := cache.ReleaseReservation(ctx, req.ProductID, 1); relErr != nil {
			releaseErr = relErr
			util.LogrusObj.Errorf("release reservation on tx fail failed: %v", relErr)
		}
		return nil, err
	}
	_ = releaseErr

	return &types.PreorderActionResp{
		OrderID:       order.ID,
		OrderNum:      order.OrderNum,
		PreorderStage: model.PreorderStageDepositPaid,
		OrderType:     consts.OrderWaitPay,
		Message:       fmt.Sprintf("定金已支付，请于 %s 前完成尾款支付", pp.FinalEndAt.Format("2006-01-02 15:04")),
	}, nil
}

// PayFinal 尾款期内付尾款：扣尾款 + 真扣库存 (reserved -> sold) + 订单转 WaitShip + outbox。
//   - 时间窗校验：now ∈ [DepositEndAt, FinalEndAt)
//   - PreorderStage 必须为 DepositPaid
//   - 库存层面只在尾款阶段才真正消耗 product.Num（沿用 payment.go 口径）
func (s *PreorderSrv) PayFinal(ctx context.Context, req *types.PreorderFinalReq) (*types.PreorderActionResp, error) {
	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		return nil, err
	}
	if len(req.Key) != consts.EncryptMoneyKeyLength {
		return nil, errors.New("支付密码长度错误")
	}

	baseDao := dao.NewOrderDao(ctx)
	order, err := baseDao.GetOrderById(req.OrderID, u.Id)
	if err != nil {
		return nil, err
	}
	if order == nil || order.ID == 0 {
		return nil, errors.New("订单不存在")
	}
	if order.PreorderStage == model.PreorderStageForfeited {
		return nil, newCodedError(e.ErrPreorderForfeitedDeposit)
	}
	if order.PreorderStage != model.PreorderStageDepositPaid {
		return nil, newCodedError(e.ErrPreorderDepositNotPaid)
	}

	pp, err := dao.NewPreorderDao(ctx).GetPreorderByProductID(order.ProductID)
	if err != nil {
		return nil, err
	}
	now := nowFn()
	if now.Before(pp.DepositEndAt) || !now.Before(pp.FinalEndAt) {
		return nil, newCodedError(e.ErrPreorderNotInFinalWindow)
	}

	err = baseDao.DB.Transaction(func(tx *gorm.DB) error {
		// 1) 扣尾款
		if e := debitUser(tx, u.Id, order.BossID, req.Key, pp.FinalCents); e != nil {
			return e
		}

		// 2) 真扣商品库存（DB 层面）。沿用 payment.go 的"读 -> 减 -> 更新"路径；
		//    高并发由更上层 Redis reserved 桶兜底，这里 product.Num 是历史水位。
		productDao := dao.NewProductDaoByDB(tx)
		product, e := productDao.GetProductById(order.ProductID)
		if e != nil {
			return e
		}
		if product.Num-order.Num < 0 {
			return errors.New("存在超卖问题")
		}
		product.Num -= order.Num
		if e := productDao.UpdateProduct(order.ProductID, product); e != nil {
			return e
		}

		// 3) 状态机推进：preorder_stage 1->2, type WaitPay->WaitShip
		ok, e := dao.NewPreorderDaoByDB(tx).MarkFinalPaid(tx, order.ID, now)
		if e != nil {
			return e
		}
		if !ok {
			return errors.New("尾款状态机推进失败：订单状态已变更")
		}

		// 4) outbox 事件
		return dao.NewOutboxDaoByDB(tx).Insert(
			"order", "PreorderFinalPaid", "preorder.final.paid", order.ID,
			events.PreorderFinalPaid{
				OrderID:   order.ID,
				OrderNum:  order.OrderNum,
				UserID:    u.Id,
				ProductID: order.ProductID,
				Final:     pp.FinalCents,
				Total:     pp.DepositCents + pp.FinalCents,
			},
		)
	})
	if err != nil {
		return nil, err
	}

	// 事务成功后再清 Redis reserved，cache 失败不回滚 DB（业务真相在 DB）
	if cErr := cache.CommitReservation(ctx, order.ProductID, int64(order.Num)); cErr != nil {
		util.LogrusObj.Errorf("commit reservation on final paid failed orderID=%d err=%v", order.ID, cErr)
	}

	return &types.PreorderActionResp{
		OrderID:       order.ID,
		OrderNum:      order.OrderNum,
		PreorderStage: model.PreorderStageFinalPaid,
		OrderType:     consts.OrderWaitShip,
		Message:       "尾款已支付，等待商家发货",
	}, nil
}

// CancelPreorderInDepositWindow 仅在定金期内可全退；预售结束后调用返回 ErrPreorderForfeitedDeposit。
//   - 时间窗校验：now ∈ [DepositStartAt, DepositEndAt)
//   - 把订单整体回退到 Closed + 释放库存 + 退回定金 + 写 outbox(preorder.cancelled)
func (s *PreorderSrv) CancelPreorderInDepositWindow(ctx context.Context, req *types.PreorderCancelReq) (*types.PreorderActionResp, error) {
	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		return nil, err
	}
	if len(req.Key) != consts.EncryptMoneyKeyLength {
		return nil, errors.New("支付密码长度错误")
	}

	baseDao := dao.NewOrderDao(ctx)
	order, err := baseDao.GetOrderById(req.OrderID, u.Id)
	if err != nil {
		return nil, err
	}
	if order == nil || order.ID == 0 {
		return nil, errors.New("订单不存在")
	}
	if order.PreorderStage != model.PreorderStageDepositPaid {
		// 已付尾款或已没收，不再走"全退"路径
		if order.PreorderStage == model.PreorderStageForfeited {
			return nil, newCodedError(e.ErrPreorderForfeitedDeposit)
		}
		return nil, errors.New("订单不在可取消的阶段")
	}

	pp, err := dao.NewPreorderDao(ctx).GetPreorderByProductID(order.ProductID)
	if err != nil {
		return nil, err
	}
	now := nowFn()
	if now.Before(pp.DepositStartAt) || !now.Before(pp.DepositEndAt) {
		// 已过定金期：业务上是"定金不退"
		return nil, newCodedError(e.ErrPreorderForfeitedDeposit)
	}

	err = baseDao.DB.Transaction(func(tx *gorm.DB) error {
		// 1) 全额退还定金到用户
		if e := refundUser(tx, u.Id, order.BossID, req.Key, pp.DepositCents); e != nil {
			return e
		}
		// 2) 状态机重置：preorder_stage 1->0, type WaitPay->Closed
		ok, e := dao.NewPreorderDaoByDB(tx).ResetPreorderOnCancel(tx, order.ID)
		if e != nil {
			return e
		}
		if !ok {
			return errors.New("取消失败：订单状态已变更")
		}
		// 3) outbox
		return dao.NewOutboxDaoByDB(tx).Insert(
			"order", "PreorderCancelled", "preorder.cancelled", order.ID,
			events.PreorderCancelled{
				OrderID:   order.ID,
				OrderNum:  order.OrderNum,
				UserID:    u.Id,
				ProductID: order.ProductID,
				Refund:    pp.DepositCents,
			},
		)
	})
	if err != nil {
		return nil, err
	}

	// 事务成功后释放 reserved（与 order_cancel.go 同口径）
	if relErr := cache.ReleaseReservation(ctx, order.ProductID, int64(order.Num)); relErr != nil {
		util.LogrusObj.Errorf("release reservation on preorder cancel failed orderID=%d err=%v", order.ID, relErr)
	}

	return &types.PreorderActionResp{
		OrderID:       order.ID,
		OrderNum:      order.OrderNum,
		PreorderStage: model.PreorderStageNone,
		OrderType:     consts.OrderClosed,
		Message:       "已在定金期内取消，定金已原路退回",
	}, nil
}

// ForfeitDepositsForUnpaidFinals cron 入口：扫所有 FinalEndAt 已过但 stage 仍停在 DepositPaid 的订单。
//   - 单笔事务：标 stage=Forfeited / type=Closed + outbox
//   - 事务成功后释放 reserved 库存（cache 失败不回滚 DB）
//   - 定金不退给用户：用户的钱在 PayDeposit 已经划给商家，没收逻辑仅写事件 + 状态机，
//     真正的"定金划归平台收益"路线图阶段由 wallet 服务消费 preorder.forfeited 实现
func (s *PreorderSrv) ForfeitDepositsForUnpaidFinals(ctx context.Context) error {
	ppDao := dao.NewPreorderDao(ctx)
	ids, err := ppDao.ListUnpaidFinalBefore(nowFn(), 200)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	for _, id := range ids {
		if err := s.forfeitOne(ctx, id); err != nil {
			util.LogrusObj.Errorf("forfeit preorder failed orderID=%d err=%v", id, err)
			continue
		}
	}
	return nil
}

func (s *PreorderSrv) forfeitOne(ctx context.Context, orderID uint) error {
	baseDao := dao.NewOrderDao(ctx)
	var (
		productID uint
		num       int
		orderNum  uint64
		userID    uint
		deposit   int64
	)
	err := baseDao.DB.Transaction(func(tx *gorm.DB) error {
		// 重新读一次拿快照
		var order model.Order
		if e := tx.Model(&model.Order{}).Where("id=?", orderID).First(&order).Error; e != nil {
			return e
		}
		if order.PreorderStage != model.PreorderStageDepositPaid {
			return nil // 已被处理过，幂等返回
		}
		pp, e := dao.NewPreorderDaoByDB(tx).GetPreorderByProductID(order.ProductID)
		if e != nil {
			return e
		}

		ok, e := dao.NewPreorderDaoByDB(tx).ForfeitDeposit(tx, order.ID)
		if e != nil {
			return e
		}
		if !ok {
			return nil // 状态已变（用户卡点付了），跳过
		}

		productID = order.ProductID
		num = order.Num
		orderNum = order.OrderNum
		userID = order.UserID
		deposit = pp.DepositCents

		return dao.NewOutboxDaoByDB(tx).Insert(
			"order", "PreorderForfeited", "preorder.forfeited", order.ID,
			events.PreorderForfeited{
				OrderID:   order.ID,
				OrderNum:  order.OrderNum,
				UserID:    order.UserID,
				ProductID: order.ProductID,
				Deposit:   pp.DepositCents,
			},
		)
	})
	if err != nil {
		return err
	}
	if productID == 0 {
		return nil
	}
	// 库存归还 reserved -> available（与超时关单同口径）
	if relErr := cache.ReleaseReservation(ctx, productID, int64(num)); relErr != nil {
		util.LogrusObj.Errorf("release reservation on forfeit failed orderID=%d err=%v", orderID, relErr)
	}
	_ = orderNum
	_ = userID
	_ = deposit
	return nil
}

// ShowPreorder 公共预售信息展示，不校验登录。
// phase 字段直接给前端使用：deposit / final / forfeited / not_started。
func (s *PreorderSrv) ShowPreorder(ctx context.Context, productID uint) (*types.PreorderShowResp, error) {
	pp, err := dao.NewPreorderDao(ctx).GetPreorderByProductID(productID)
	if err != nil {
		return nil, err
	}
	now := nowFn()
	phase := "not_started"
	switch {
	case now.Before(pp.DepositStartAt):
		phase = "not_started"
	case now.Before(pp.DepositEndAt):
		phase = "deposit"
	case now.Before(pp.FinalEndAt):
		phase = "final"
	default:
		phase = "forfeited"
	}
	return &types.PreorderShowResp{
		ProductID:      pp.ProductID,
		DepositCents:   pp.DepositCents,
		FinalCents:     pp.FinalCents,
		TotalCents:     pp.DepositCents + pp.FinalCents,
		DepositStartAt: pp.DepositStartAt.Unix(),
		DepositEndAt:   pp.DepositEndAt.Unix(),
		FinalEndAt:     pp.FinalEndAt.Unix(),
		ShipAt:         pp.ShipAt.Unix(),
		NowAt:          now.Unix(),
		Phase:          phase,
	}, nil
}

// ---------- 内部工具 ----------

// debitUser 把 amountCents 从 user 划到 boss，保持与 payment.go::PayDown 口径一致。
// 抽出来是为了在 PayDeposit / PayFinal 共用，避免两份 AES 重复代码。
func debitUser(tx *gorm.DB, userID, bossID uint, key string, amountCents int64) error {
	if amountCents <= 0 {
		return nil
	}
	userDao := dao.NewUserDaoByDB(tx)
	u, err := userDao.GetUserById(userID)
	if err != nil {
		return err
	}
	userMoney, err := u.DecryptMoney(key)
	if err != nil {
		return err
	}
	if userMoney-amountCents < 0 {
		return errors.New("金额不足")
	}
	u.Money = strconv.FormatInt(userMoney-amountCents, 10)
	u.Money, err = u.EncryptMoney(key)
	if err != nil {
		return err
	}
	if err := userDao.UpdateUserById(userID, u); err != nil {
		return err
	}

	boss, err := userDao.GetUserById(bossID)
	if err != nil {
		return err
	}
	bossMoney, err := boss.DecryptMoney(key)
	if err != nil {
		return err
	}
	boss.Money = strconv.FormatInt(bossMoney+amountCents, 10)
	boss.Money, err = boss.EncryptMoney(key)
	if err != nil {
		return err
	}
	return userDao.UpdateUserById(bossID, boss)
}

// refundUser 反向：boss 划回 user。仅供"定金期内取消"使用。
func refundUser(tx *gorm.DB, userID, bossID uint, key string, amountCents int64) error {
	if amountCents <= 0 {
		return nil
	}
	userDao := dao.NewUserDaoByDB(tx)
	boss, err := userDao.GetUserById(bossID)
	if err != nil {
		return err
	}
	bossMoney, err := boss.DecryptMoney(key)
	if err != nil {
		return err
	}
	if bossMoney-amountCents < 0 {
		// 商家钱不够退：业务上不可能（商家入账即冻结），出现说明账本异常
		return errors.New("商家余额异常，退款失败")
	}
	boss.Money = strconv.FormatInt(bossMoney-amountCents, 10)
	boss.Money, err = boss.EncryptMoney(key)
	if err != nil {
		return err
	}
	if err := userDao.UpdateUserById(bossID, boss); err != nil {
		return err
	}

	u, err := userDao.GetUserById(userID)
	if err != nil {
		return err
	}
	userMoney, err := u.DecryptMoney(key)
	if err != nil {
		return err
	}
	u.Money = strconv.FormatInt(userMoney+amountCents, 10)
	u.Money, err = u.EncryptMoney(key)
	if err != nil {
		return err
	}
	return userDao.UpdateUserById(userID, u)
}
