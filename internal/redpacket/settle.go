package redpacket

import (
	"context"
	"errors"
	"strconv"

	"gorm.io/gorm"

	"github.com/RedInn7/gomall/internal/money"
	"github.com/RedInn7/gomall/internal/user"
	"github.com/RedInn7/gomall/repository/db/dao"
)

// 红包资金链：复式记账，发包人 / 领取人为内部钱包，对手方统一记在平台清算账户
// money.ExternalClearingUserID（红包在途资金的 escrow），保证 SUM(debit)=SUM(credit) 守恒。
//
// 三笔资金流均"改余额 + 写台账"落在同一事务，幂等由台账唯一索引
// (ref_order_id, direction, biz_type) + 入账前存在性预检共同兜底：
//   - 发包  settleSend   ref=红包 id        发包人 debit  / 清算 credit
//   - 领包  settleClaim  ref=领取记录 id    领取人 credit / 清算 debit
//   - 回收  settleRefund ref=红包 id        发包人 credit / 清算 debit

// adjustWallet 在事务内对单个用户钱包做一次原子增减并追加一条台账。
// delta>0 入账(credit)，delta<0 出账(debit)。返回变更后余额（单位：分）。
// 已存在同 (ref, direction, biz_type) 流水则视为重复，跳过余额变动并返回 skipped=true。
func adjustWallet(tx *gorm.DB, userID, refID uint, delta int64, bizType string) (skipped bool, balanceAfter int64, err error) {
	direction := money.DirectionCredit
	if delta < 0 {
		direction = money.DirectionDebit
	}

	var existing int64
	if err = tx.Model(&money.AccountTransaction{}).
		Where("ref_order_id=? AND direction=? AND biz_type=?", refID, direction, bizType).
		Count(&existing).Error; err != nil {
		return false, 0, err
	}
	if existing > 0 {
		return true, 0, nil
	}

	userDao := user.NewUserDaoByDB(tx)
	u, err := userDao.GetUserByIdForUpdate(userID)
	if err != nil {
		return false, 0, err
	}
	bal, err := u.DecryptMoney()
	if err != nil {
		return false, 0, err
	}
	balanceAfter = bal + delta
	u.Money = strconv.FormatInt(balanceAfter, 10)
	if u.Money, err = u.EncryptMoney(); err != nil {
		return false, 0, err
	}
	if err = userDao.UpdateUserById(userID, u); err != nil {
		return false, 0, err
	}

	amount := delta
	if amount < 0 {
		amount = -amount
	}
	if err = money.NewLedgerDaoByDB(tx).AppendTransaction(userID, refID, direction, amount, balanceAfter, bizType); err != nil {
		return false, 0, err
	}
	return false, balanceAfter, nil
}

// adjustClearing 对平台清算账户（user_id=0，无加密钱包，仅记台账）追加对手方流水，守恒账。
// 清算账户不持有 users.money 行，故只 append 流水、balance_after 记 0（清算账户余额由台账自身聚合得出）。
func adjustClearing(tx *gorm.DB, refID uint, delta int64, bizType string) error {
	direction := money.DirectionCredit
	if delta < 0 {
		direction = money.DirectionDebit
	}
	amount := delta
	if amount < 0 {
		amount = -amount
	}

	var existing int64
	if err := tx.Model(&money.AccountTransaction{}).
		Where("ref_order_id=? AND direction=? AND biz_type=?", refID, direction, bizType).
		Count(&existing).Error; err != nil {
		return err
	}
	if existing > 0 {
		return nil
	}
	return money.NewLedgerDaoByDB(tx).AppendTransaction(money.ExternalClearingUserID, refID, direction, amount, 0, bizType)
}

// settleSendTx 发包扣款：发包人钱包 -total 进 escrow（清算账户 +total）。须在调用方事务内执行。
// 幂等：同一红包重复结算因台账已存在而跳过。返回 err 由调用方回滚整笔发包。
func settleSendTx(tx *gorm.DB, rpID, senderID uint, total int64) error {
	if total <= 0 {
		return errors.New("redpacket settle: invalid total")
	}
	skipped, _, err := adjustWallet(tx, senderID, rpID, -total, money.BizTypeRedPacketSend)
	if err != nil {
		return err
	}
	if skipped {
		return nil
	}
	return adjustClearing(tx, rpID, total, money.BizTypeRedPacketSend)
}

// settleClaimTx 领包入账：escrow -amount 进领取人钱包（领取人 +amount）。须在调用方事务内执行。
// ref 用领取记录 id（每个领取人唯一一行），与发/退的红包 id 空间隔离，天然防同红包不同领取人撞唯一键。
func settleClaimTx(tx *gorm.DB, claimID, receiverID uint, amount int64) error {
	if amount <= 0 {
		return errors.New("redpacket settle: invalid claim amount")
	}
	skipped, _, err := adjustWallet(tx, receiverID, claimID, amount, money.BizTypeRedPacketClaim)
	if err != nil {
		return err
	}
	if skipped {
		return nil
	}
	return adjustClearing(tx, claimID, -amount, money.BizTypeRedPacketClaim)
}

// settleRefundTx 过期回收：escrow -left 退回发包人钱包（发包人 +left）。须在调用方事务内执行。
func settleRefundTx(tx *gorm.DB, rpID, senderID uint, left int64) error {
	if left <= 0 {
		return nil
	}
	skipped, _, err := adjustWallet(tx, senderID, rpID, left, money.BizTypeRedPacketRefund)
	if err != nil {
		return err
	}
	if skipped {
		return nil
	}
	return adjustClearing(tx, rpID, -left, money.BizTypeRedPacketRefund)
}

// SettleSend 独立事务结算一笔发包资金流，供事件消费者驱动（at-least-once + 台账幂等）。
func (s *RedPacketSrv) SettleSend(ctx context.Context, rpID, senderID uint, total int64) error {
	return dao.NewDBClient(ctx).Transaction(func(tx *gorm.DB) error {
		return settleSendTx(tx, rpID, senderID, total)
	})
}

// SettleClaim 独立事务结算一笔领包入账，供事件消费者驱动。claimID 必须为领取记录 id。
func (s *RedPacketSrv) SettleClaim(ctx context.Context, claimID, receiverID uint, amount int64) error {
	return dao.NewDBClient(ctx).Transaction(func(tx *gorm.DB) error {
		return settleClaimTx(tx, claimID, receiverID, amount)
	})
}

// SettleRefund 独立事务结算一笔过期回收，供事件消费者驱动。
func (s *RedPacketSrv) SettleRefund(ctx context.Context, rpID, senderID uint, left int64) error {
	return dao.NewDBClient(ctx).Transaction(func(tx *gorm.DB) error {
		return settleRefundTx(tx, rpID, senderID, left)
	})
}
