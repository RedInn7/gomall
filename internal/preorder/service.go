package preorder

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/internal/address"
	"github.com/RedInn7/gomall/internal/money"
	"github.com/RedInn7/gomall/internal/order"
	"github.com/RedInn7/gomall/internal/product"
	"github.com/RedInn7/gomall/internal/shared/outbox"
	"github.com/RedInn7/gomall/internal/user"
	"github.com/RedInn7/gomall/pkg/e"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/pkg/utils/snowflake"
	"github.com/RedInn7/gomall/repository/cache"
	"github.com/RedInn7/gomall/repository/db/dao"
	"github.com/RedInn7/gomall/service/events"
)

// PreorderSrv 预售两段式支付。
// 三个写动作（付定金 / 付尾款 / 取消）共享一组不变量：
//  1. 时间窗口校验在事务之外，业务码直接 4xx
//  2. 真正的余额扣减 + 库存动作 + 状态机推进 + outbox 写入在同一事务
//  3. 失败路径走 Saga：cache 操作放在事务外，事务失败时回滚 cache
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

// 时钟函数指针，方便测试替换。生产路径恒为 time.Now。
var nowFn = time.Now

// PayDeposit 定金期内付定金：建预售订单 + 锁库存 + 扣定金 + 写 outbox。
//   - 时间窗校验：now ∈ [DepositStartAt, DepositEndAt)
//   - 复用 inventory Lua 的 reserve 桶：定金期占住库存但不真扣
//   - 失败路径：事务失败 → ReleaseReservation 回滚 reserved
func (s *PreorderSrv) PayDeposit(ctx context.Context, req *PreorderDepositReq) (*PreorderActionResp, error) {
	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		return nil, err
	}
	if len(req.Key) != consts.EncryptMoneyKeyLength {
		return nil, errors.New("支付密码长度错误")
	}

	// 1) 查预售配置 + 窗口校验
	pp, err := NewPreorderDao(ctx).GetPreorderByProductID(req.ProductID)
	if err != nil {
		return nil, err
	}
	now := nowFn()
	if now.Before(pp.DepositStartAt) || !now.Before(pp.DepositEndAt) {
		return nil, e.New(e.ErrPreorderNotInDepositWindow)
	}

	// 卖家（定金的收款方）以商品表为准，忽略 req.BossID：定金会打进 boss 钱包，
	// 信客户端的 boss_id 等于让买家把货款转给任意账户。
	bossID, err := product.NewProductDao(ctx).ResolveBossID(req.ProductID)
	if err != nil {
		util.LogrusObj.Errorf("preorder resolve boss failed product=%d err=%v", req.ProductID, err)
		return nil, err
	}

	// 收货地址必须属于当前用户，不能信客户端传入的 address_id
	if err = address.NewAddressDao(ctx).EnsureOwned(req.AddressID, u.Id); err != nil {
		util.LogrusObj.Errorf("preorder address ownership check failed addr=%d user=%d err=%v", req.AddressID, u.Id, err)
		return nil, err
	}

	// 2) Redis reserve（available -> reserved）；预售单数固定 1（业务约束：1 单 1 件）
	if err := cache.ReserveStock(ctx, req.ProductID, 1); err != nil {
		util.LogrusObj.Errorf("preorder reserve stock failed product=%d err=%v", req.ProductID, err)
		return nil, err
	}

	// 3) 同事务：建订单 + 扣定金 + Mark + outbox
	var (
		ord        = &order.Order{}
		releaseErr error
	)
	err = dao.NewDBClient(ctx).Transaction(func(tx *gorm.DB) error {
		ord.UserID = u.Id
		ord.ProductID = req.ProductID
		ord.BossID = bossID // 卖家以商品表为准，忽略 req.BossID
		ord.AddressID = req.AddressID
		ord.Num = 1
		ord.Money = pp.DepositCents + pp.FinalCents // 累计金额；分两次扣
		ord.Type = consts.OrderWaitPay
		ord.OrderNum = uint64(snowflake.GenSnowflakeID())

		if e := order.NewOrderDaoByDB(tx).CreateOrder(ord); e != nil {
			return e
		}

		if e := debitUser(tx, u.Id, bossID, req.Key, pp.DepositCents, ord.ID, money.BizTypePreorderDeposit); e != nil {
			return e
		}

		paidAt := now
		ok, e := NewPreorderDaoByDB(tx).MarkDepositPaid(tx, ord.ID, paidAt)
		if e != nil {
			return e
		}
		if !ok {
			return errors.New("preorder stage 推进失败：订单状态已变更")
		}

		return outbox.NewOutboxDaoByDB(tx).Insert(
			"order", "PreorderDepositPaid", "preorder.deposit.paid", ord.ID,
			events.PreorderDepositPaid{
				OrderID:   ord.ID,
				OrderNum:  ord.OrderNum,
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

	return &PreorderActionResp{
		OrderID:       ord.ID,
		OrderNum:      ord.OrderNum,
		PreorderStage: PreorderStageDepositPaid,
		OrderType:     consts.OrderWaitPay,
		Message:       fmt.Sprintf("定金已支付，请于 %s 前完成尾款支付", pp.FinalEndAt.Format("2006-01-02 15:04")),
	}, nil
}

// PayFinal 尾款期内付尾款：扣尾款 + 真扣库存 (reserved -> sold) + 订单转 WaitShip + outbox。
//   - 时间窗校验：now ∈ [DepositEndAt, FinalEndAt)
//   - PreorderStage 必须为 DepositPaid
//   - 库存层面只在尾款阶段才真正消耗 product.Num（沿用 payment.go 口径）
func (s *PreorderSrv) PayFinal(ctx context.Context, req *PreorderFinalReq) (*PreorderActionResp, error) {
	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		return nil, err
	}
	if len(req.Key) != consts.EncryptMoneyKeyLength {
		return nil, errors.New("支付密码长度错误")
	}

	baseDao := order.NewOrderDao(ctx)
	ord, err := baseDao.GetOrderById(req.OrderID, u.Id)
	if err != nil {
		return nil, err
	}
	if ord == nil || ord.ID == 0 {
		return nil, errors.New("订单不存在")
	}
	if ord.PreorderStage == PreorderStageForfeited {
		return nil, e.New(e.ErrPreorderForfeitedDeposit)
	}
	if ord.PreorderStage != PreorderStageDepositPaid {
		return nil, e.New(e.ErrPreorderDepositNotPaid)
	}

	pp, err := NewPreorderDao(ctx).GetPreorderByProductID(ord.ProductID)
	if err != nil {
		return nil, err
	}
	now := nowFn()
	if now.Before(pp.DepositEndAt) || !now.Before(pp.FinalEndAt) {
		return nil, e.New(e.ErrPreorderNotInFinalWindow)
	}

	err = baseDao.DB.Transaction(func(tx *gorm.DB) error {
		// 1) 扣尾款
		if e := debitUser(tx, u.Id, ord.BossID, req.Key, pp.FinalCents, ord.ID, money.BizTypePreorderFinal); e != nil {
			return e
		}

		// 2) 真扣商品库存（DB 层面），原子条件 UPDATE 消除并发超卖。
		ok, e := product.NewProductDaoWithDB(tx).DeductStock(ord.ProductID, ord.Num)
		if e != nil {
			return e
		}
		if !ok {
			return errors.New("存在超卖问题")
		}

		// 3) 状态机推进：preorder_stage 1->2, type WaitPay->WaitShip
		ok, e = NewPreorderDaoByDB(tx).MarkFinalPaid(tx, ord.ID, now)
		if e != nil {
			return e
		}
		if !ok {
			return errors.New("尾款状态机推进失败：订单状态已变更")
		}

		// 4) outbox 事件
		return outbox.NewOutboxDaoByDB(tx).Insert(
			"order", "PreorderFinalPaid", "preorder.final.paid", ord.ID,
			events.PreorderFinalPaid{
				OrderID:   ord.ID,
				OrderNum:  ord.OrderNum,
				UserID:    u.Id,
				ProductID: ord.ProductID,
				Final:     pp.FinalCents,
				Total:     pp.DepositCents + pp.FinalCents,
			},
		)
	})
	if err != nil {
		return nil, err
	}

	// 事务成功后再清 Redis reserved，cache 失败不回滚 DB（业务真相在 DB）
	if cErr := cache.CommitReservation(ctx, ord.ProductID, int64(ord.Num)); cErr != nil {
		util.LogrusObj.Errorf("commit reservation on final paid failed orderID=%d err=%v", ord.ID, cErr)
	}

	return &PreorderActionResp{
		OrderID:       ord.ID,
		OrderNum:      ord.OrderNum,
		PreorderStage: PreorderStageFinalPaid,
		OrderType:     consts.OrderWaitShip,
		Message:       "尾款已支付，等待商家发货",
	}, nil
}

// CancelPreorderInDepositWindow 仅在定金期内可全退；预售结束后调用返回 ErrPreorderForfeitedDeposit。
//   - 时间窗校验：now ∈ [DepositStartAt, DepositEndAt)
//   - 把订单整体回退到 Closed + 释放库存 + 退回定金 + 写 outbox(preorder.cancelled)
func (s *PreorderSrv) CancelPreorderInDepositWindow(ctx context.Context, req *PreorderCancelReq) (*PreorderActionResp, error) {
	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		return nil, err
	}
	if len(req.Key) != consts.EncryptMoneyKeyLength {
		return nil, errors.New("支付密码长度错误")
	}

	baseDao := order.NewOrderDao(ctx)
	ord, err := baseDao.GetOrderById(req.OrderID, u.Id)
	if err != nil {
		return nil, err
	}
	if ord == nil || ord.ID == 0 {
		return nil, errors.New("订单不存在")
	}
	if ord.PreorderStage != PreorderStageDepositPaid {
		// 已付尾款或已没收，不再走"全退"路径
		if ord.PreorderStage == PreorderStageForfeited {
			return nil, e.New(e.ErrPreorderForfeitedDeposit)
		}
		return nil, errors.New("订单不在可取消的阶段")
	}

	pp, err := NewPreorderDao(ctx).GetPreorderByProductID(ord.ProductID)
	if err != nil {
		return nil, err
	}
	now := nowFn()
	if now.Before(pp.DepositStartAt) || !now.Before(pp.DepositEndAt) {
		// 已过定金期：业务上是"定金不退"
		return nil, e.New(e.ErrPreorderForfeitedDeposit)
	}

	err = baseDao.DB.Transaction(func(tx *gorm.DB) error {
		// 1) 全额退还定金到用户
		if e := refundUser(tx, u.Id, ord.BossID, req.Key, pp.DepositCents, ord.ID, money.BizTypePreorderRefund); e != nil {
			return e
		}
		// 2) 状态机重置：preorder_stage 1->0, type WaitPay->Closed
		ok, e := NewPreorderDaoByDB(tx).ResetPreorderOnCancel(tx, ord.ID)
		if e != nil {
			return e
		}
		if !ok {
			return errors.New("取消失败：订单状态已变更")
		}
		// 3) outbox
		return outbox.NewOutboxDaoByDB(tx).Insert(
			"order", "PreorderCancelled", "preorder.cancelled", ord.ID,
			events.PreorderCancelled{
				OrderID:   ord.ID,
				OrderNum:  ord.OrderNum,
				UserID:    u.Id,
				ProductID: ord.ProductID,
				Refund:    pp.DepositCents,
			},
		)
	})
	if err != nil {
		return nil, err
	}

	// 事务成功后释放 reserved（与 order_cancel.go 同口径）
	if relErr := cache.ReleaseReservation(ctx, ord.ProductID, int64(ord.Num)); relErr != nil {
		util.LogrusObj.Errorf("release reservation on preorder cancel failed orderID=%d err=%v", ord.ID, relErr)
	}

	return &PreorderActionResp{
		OrderID:       ord.ID,
		OrderNum:      ord.OrderNum,
		PreorderStage: PreorderStageNone,
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
	ppDao := NewPreorderDao(ctx)
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
	baseDao := order.NewOrderDao(ctx)
	var (
		productID uint
		num       int
		orderNum  uint64
		userID    uint
		deposit   int64
	)
	err := baseDao.DB.Transaction(func(tx *gorm.DB) error {
		// 重新读一次拿快照
		var ord order.Order
		if e := tx.Model(&order.Order{}).Where("id=?", orderID).First(&ord).Error; e != nil {
			return e
		}
		if ord.PreorderStage != PreorderStageDepositPaid {
			return nil // 已被处理过，幂等返回
		}
		pp, e := NewPreorderDaoByDB(tx).GetPreorderByProductID(ord.ProductID)
		if e != nil {
			return e
		}

		ok, e := NewPreorderDaoByDB(tx).ForfeitDeposit(tx, ord.ID)
		if e != nil {
			return e
		}
		if !ok {
			return nil // 状态已变（用户卡点付了），跳过
		}

		productID = ord.ProductID
		num = ord.Num
		orderNum = ord.OrderNum
		userID = ord.UserID
		deposit = pp.DepositCents

		return outbox.NewOutboxDaoByDB(tx).Insert(
			"order", "PreorderForfeited", "preorder.forfeited", ord.ID,
			events.PreorderForfeited{
				OrderID:   ord.ID,
				OrderNum:  ord.OrderNum,
				UserID:    ord.UserID,
				ProductID: ord.ProductID,
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
func (s *PreorderSrv) ShowPreorder(ctx context.Context, productID uint) (*PreorderShowResp, error) {
	pp, err := NewPreorderDao(ctx).GetPreorderByProductID(productID)
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
	return &PreorderShowResp{
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
//
// 余额密文落库不可对账，故在同一事务内为这笔买家→卖家的资金转移追加成对台账流水：
// 买家 debit + 卖家 credit，bizType 区分定金 / 尾款两阶段。balance_after 记各方变更后余额，
// (ref_order_id, direction, biz_type) 唯一索引兜底，事务失败一起回滚。
func debitUser(tx *gorm.DB, userID, bossID uint, key string, amountCents int64, refOrderID uint, bizType string) error {
	if amountCents <= 0 {
		return nil
	}
	userDao := user.NewUserDaoByDB(tx)
	ledgerDao := money.NewLedgerDaoByDB(tx)
	// 加行锁读买家行，先校验支付密码，再做服务端密钥的余额读改写
	u, err := userDao.GetUserByIdForUpdate(userID)
	if err != nil {
		return err
	}
	if !u.CheckMoneyPassword(key) {
		return user.ErrMoneyKeyIncorrect
	}
	userMoney, err := u.DecryptMoney()
	if err != nil {
		return err
	}
	if userMoney-amountCents < 0 {
		return errors.New("金额不足")
	}
	buyerBalanceAfter := userMoney - amountCents
	u.Money = strconv.FormatInt(buyerBalanceAfter, 10)
	u.Money, err = u.EncryptMoney()
	if err != nil {
		return err
	}
	if err := userDao.UpdateUserById(userID, u); err != nil {
		return err
	}
	// 买家扣款流水：与余额变动同事务追加
	if err := ledgerDao.AppendTransaction(userID, refOrderID, money.DirectionDebit, amountCents, buyerBalanceAfter, bizType); err != nil {
		return err
	}

	boss, err := userDao.GetUserByIdForUpdate(bossID)
	if err != nil {
		return err
	}
	// 商家余额同样用服务端密钥解密，绝不能用买家支付密码
	bossMoney, err := boss.DecryptMoney()
	if err != nil {
		return err
	}
	bossBalanceAfter := bossMoney + amountCents
	boss.Money = strconv.FormatInt(bossBalanceAfter, 10)
	boss.Money, err = boss.EncryptMoney()
	if err != nil {
		return err
	}
	if err := userDao.UpdateUserById(bossID, boss); err != nil {
		return err
	}
	// 卖家入账流水：方向与买家相反，与买家流水配对保持 SUM(debit)=SUM(credit)
	return ledgerDao.AppendTransaction(bossID, refOrderID, money.DirectionCredit, amountCents, bossBalanceAfter, bizType)
}

// refundUser 反向：boss 划回 user。仅供"定金期内取消"使用。
//
// 退款是定金的反向转移（卖家→买家），同一事务内追加成对台账流水：卖家 debit + 买家 credit，
// bizType=preorder_refund。该 biz_type 与定金 / 尾款不同，故同一订单上退款流水与定金 debit
// 不冲突；(ref_order_id, direction, biz_type) 唯一索引兜底，事务失败一起回滚。
func refundUser(tx *gorm.DB, userID, bossID uint, key string, amountCents int64, refOrderID uint, bizType string) error {
	if amountCents <= 0 {
		return nil
	}
	userDao := user.NewUserDaoByDB(tx)
	ledgerDao := money.NewLedgerDaoByDB(tx)
	// 退款由买家发起，先校验买家支付密码
	u, err := userDao.GetUserByIdForUpdate(userID)
	if err != nil {
		return err
	}
	if !u.CheckMoneyPassword(key) {
		return user.ErrMoneyKeyIncorrect
	}

	boss, err := userDao.GetUserByIdForUpdate(bossID)
	if err != nil {
		return err
	}
	bossMoney, err := boss.DecryptMoney()
	if err != nil {
		return err
	}
	if bossMoney-amountCents < 0 {
		// 商家钱不够退：业务上不可能（商家入账即冻结），出现说明账本异常
		return errors.New("商家余额异常，退款失败")
	}
	bossBalanceAfter := bossMoney - amountCents
	boss.Money = strconv.FormatInt(bossBalanceAfter, 10)
	boss.Money, err = boss.EncryptMoney()
	if err != nil {
		return err
	}
	if err := userDao.UpdateUserById(bossID, boss); err != nil {
		return err
	}
	// 卖家退款出账流水
	if err := ledgerDao.AppendTransaction(bossID, refOrderID, money.DirectionDebit, amountCents, bossBalanceAfter, bizType); err != nil {
		return err
	}

	userMoney, err := u.DecryptMoney()
	if err != nil {
		return err
	}
	buyerBalanceAfter := userMoney + amountCents
	u.Money = strconv.FormatInt(buyerBalanceAfter, 10)
	u.Money, err = u.EncryptMoney()
	if err != nil {
		return err
	}
	if err := userDao.UpdateUserById(userID, u); err != nil {
		return err
	}
	// 买家退款入账流水：与卖家出账配对保持 SUM(debit)=SUM(credit)
	return ledgerDao.AppendTransaction(userID, refOrderID, money.DirectionCredit, amountCents, buyerBalanceAfter, bizType)
}
