package groupbuy

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"gorm.io/gorm"

	"github.com/RedInn7/gomall/internal/clearing"
	"github.com/RedInn7/gomall/internal/money"
	orderpkg "github.com/RedInn7/gomall/internal/order"
	"github.com/RedInn7/gomall/internal/user"
	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/db/dao"
)

// 拼团资金分三段流转，每段在台账上是一对方向相反、SUM(debit)=SUM(credit) 守恒的流水。
// 三段用不同的 biz_type，使同一订单（ref_order_id）上三段互不冲突，
// 又能各自命中 (ref_order_id, direction, biz_type) 唯一索引做幂等兜底：
//
//	加入 → 成员钱包 debit、groupbuy_escrow credit（钱先托管在平台，不直接进卖家）
//	成团 → groupbuy_escrow debit、merchant_escrow credit + 清算单（凑齐人数才算成交，但仍要等履约完成才结算给卖家）
//	散团 / 关团 → groupbuy_escrow debit、成员钱包 credit（没成团，托管款原路退回成员）
//
// 托管账户是 user_id=0 的系统账户，用独立的 account_code=groupbuy_escrow，与外部清算、商户托管、红包分开对账；
// 系统账户不维护钱包行，流水 balance_after 记 0。
const (
	BizTypeGroupBuyPay    = "groupbuy_pay"    // 加入收款：成员 debit / 拼团托管 credit
	BizTypeGroupBuySettle = "groupbuy_settle" // 成团转清算：拼团托管 debit / 商户托管 credit
	BizTypeGroupBuyRefund = "groupbuy_refund" // 散团退款：拼团托管 debit / 成员 credit
)

// escrowAccount 拼团在途托管账户。
const escrowAccount = money.AccountCodeGroupbuyEscrow

// collectToEscrow 加入拼团时收款：成员钱包扣 amount，钱进拼团托管账户。
//
// 必须由调用方在 join 的同一事务内调用：与 member / 订单写入同生共死，
// 任一失败整体回滚，杜绝"加入成功但没扣到钱"。
//   - 成员 debit：行锁读余额 -amount 加密写回 + 追加 debit 流水(groupbuy_pay)
//   - 托管 credit：仅追加 credit 流水(groupbuy_pay)，系统账户不维护钱包行
//
// 幂等：(refOrderID, debit, groupbuy_pay) 唯一索引兜底，重复点击 / 重复投递不重复扣款。
// 拼团开团 / 参团在 service 层先过支付密码（checkPayKey，与 paydown 共用失败锁定），加入即代扣，
// 故这里不校验 money key，仅做服务端余额读改写。
func collectToEscrow(tx *gorm.DB, memberID uint, amountCents int64, refOrderID uint) error {
	if amountCents <= 0 {
		return nil
	}
	userDao := user.NewUserDaoByDB(tx)
	ledgerDao := money.NewLedgerDaoByDB(tx)

	u, err := userDao.GetUserByIdForUpdate(memberID)
	if err != nil {
		return err
	}
	bal, err := u.DecryptMoney()
	if err != nil {
		return err
	}
	if bal-amountCents < 0 {
		return errors.New("余额不足，无法加入拼团")
	}
	after := bal - amountCents
	u.Money = strconv.FormatInt(after, 10)
	if u.Money, err = u.EncryptMoney(); err != nil {
		return err
	}
	if err = userDao.UpdateUserMoney(memberID, u.Money); err != nil {
		return err
	}
	if err = ledgerDao.AppendTransaction(memberID, refOrderID, money.DirectionDebit, amountCents, after, BizTypeGroupBuyPay); err != nil {
		return err
	}
	return ledgerDao.AppendSystemTransaction(escrowAccount, refOrderID, money.DirectionCredit, amountCents, BizTypeGroupBuyPay)
}

// clearToMerchantEscrow 成团：把该成员订单的托管款从拼团托管转进商户托管，并建清算单。
//
// 由 MarkGroupSuccess 在推进订单 WaitGroup→WaitShip 的同一事务内调用。卖家此时拿不到钱：
// 订单 Completed 后由 clearing 结算消费者放款，发货前退款走 merchant_escrow 原路退回，不会扣成卖家负余额。
// 幂等：清算单 order_id 唯一 + (ref, direction, groupbuy_settle) 唯一索引；成团本身由 status=open 条件更新保证只跑一次。
func clearToMerchantEscrow(tx *gorm.DB, o *orderpkg.Order) error {
	return clearing.RecordClearedFromSystemTx(tx, o, escrowAccount, BizTypeGroupBuySettle)
}

// refundFromEscrow 散团 / 关团退款：把托管款从拼团托管账户原路退回成员钱包。
//
// 由 ExpireGroup 在关单（WaitGroup→Closed）的同一事务内调用，与库存归还、状态翻转同生共死。
//   - 托管 debit：仅追加 debit 流水(groupbuy_refund)
//   - 成员 credit：行锁读余额 +amount 加密写回 + 追加 credit 流水(groupbuy_refund)
//
// 幂等：(refOrderID, credit, groupbuy_refund) 唯一索引 + 入账前存在性预检，重复散团不重复退款。
func refundFromEscrow(tx *gorm.DB, memberID uint, amountCents int64, refOrderID uint) error {
	if amountCents <= 0 {
		return nil
	}
	ledgerDao := money.NewLedgerDaoByDB(tx)
	// 幂等预检：成员 credit 流水已存在即视为已退过。
	refunded, err := ledgerCount(tx, refOrderID, money.DirectionCredit, BizTypeGroupBuyRefund)
	if err != nil {
		return err
	}
	if refunded > 0 {
		return nil
	}

	if err = ledgerDao.AppendSystemTransaction(escrowAccount, refOrderID, money.DirectionDebit, amountCents, BizTypeGroupBuyRefund); err != nil {
		return err
	}

	userDao := user.NewUserDaoByDB(tx)
	u, err := userDao.GetUserByIdForUpdate(memberID)
	if err != nil {
		return err
	}
	bal, err := u.DecryptMoney()
	if err != nil {
		return err
	}
	after := bal + amountCents
	u.Money = strconv.FormatInt(after, 10)
	if u.Money, err = u.EncryptMoney(); err != nil {
		return err
	}
	if err = userDao.UpdateUserMoney(memberID, u.Money); err != nil {
		return err
	}
	return ledgerDao.AppendTransaction(memberID, refOrderID, money.DirectionCredit, amountCents, after, BizTypeGroupBuyRefund)
}

// ledgerCount 在 tx 内统计某订单某方向某业务的流水条数，用于入账前幂等预检。
func ledgerCount(tx *gorm.DB, refOrderID uint, direction, bizType string) (int64, error) {
	var n int64
	err := tx.Model(&money.AccountTransaction{}).
		Where("ref_order_id=? AND direction=? AND biz_type=?", refOrderID, direction, bizType).
		Count(&n).Error
	return n, err
}

// SettleExpiredRefund 自愈兜底：对一个已散团（或仍 open 待散）的团，确保每个成员的托管款都已退回。
//
// 同步散团路径（ExpireGroup）已在事务内退款，这里是消费者侧的 belt-and-suspenders：
// 重放 groupbuy.expired 事件时，对每个成员订单做幂等退款，已退过的靠 refundFromEscrow 的
// 存在性预检直接放行。只处理已确为散团（GroupbuyStatusExpired）的团，避免与成团结算抢账。
func (s *GroupbuySrv) SettleExpiredRefund(ctx context.Context, groupID uint) error {
	gbDao := NewGroupbuyDao(ctx)
	g, err := gbDao.GetGroupByID(groupID)
	if err != nil {
		return err
	}
	if g == nil {
		util.LogrusObj.Warnf("groupbuy settle skip: group=%d not found", groupID)
		return nil
	}
	if g.Status != GroupbuyStatusExpired {
		// 仅对已散团做退款兜底；open（尚未散）/ success / closed 一律幂等放行，
		// 不在消费者里推进状态机——散团状态推进归 ExpireGroup，避免双写冲突。
		util.LogrusObj.Infof("groupbuy settle skip: group=%d status=%d not expired", groupID, g.Status)
		return nil
	}

	members, err := gbDao.ListMembers(groupID)
	if err != nil {
		return err
	}

	return dao.NewDBClient(ctx).Transaction(func(tx *gorm.DB) error {
		orderDao := orderpkg.NewOrderDaoByDB(tx)
		for _, m := range members {
			o, e := orderDao.GetOrderByIdOnly(uint(m.OrderID))
			if e != nil {
				return e
			}
			if o == nil || o.ID == 0 {
				util.LogrusObj.Warnf("groupbuy settle skip member: order=%d not found", m.OrderID)
				continue
			}
			if e = refundFromEscrow(tx, o.UserID, o.Money, o.ID); e != nil {
				return fmt.Errorf("groupbuy refund member order=%d: %w", o.ID, e)
			}
		}
		return nil
	})
}
