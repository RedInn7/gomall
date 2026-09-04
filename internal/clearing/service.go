package clearing

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/internal/money"
	"github.com/RedInn7/gomall/internal/order"
	"github.com/RedInn7/gomall/internal/user"
	"github.com/RedInn7/gomall/repository/db/dao"
)

var (
	ErrInvalidClearingInput  = errors.New("清算参数不合法")
	ErrOrderNotCompleted     = errors.New("订单尚未完成，不能结算给卖家")
	ErrInvalidClearingState  = errors.New("清算记录状态不允许结算")
	ErrClearingOrderMismatch = errors.New("清算记录与订单不一致")
)

// RecordClearedTx 在支付事务中记录“钱已经收妥并进入商户托管”。
//
// walletBalanceAfter 非 nil 表示余额支付：调用方已经在同一 tx 内扣完买家余额，
// 这里补买家 debit 流水；nil 表示 Stripe/Web3/Xcash 外部支付，debit 记到 external_clearing。
// 两种渠道都会 credit merchant_escrow，卖家钱包此时不会入账。
func RecordClearedTx(tx *gorm.DB, o *order.Order, channel, providerRef, currency string, walletBalanceAfter *int64) error {
	if tx == nil || o == nil || o.ID == 0 || o.UserID == 0 || o.BossID == 0 || o.Num <= 0 {
		return ErrInvalidClearingInput
	}
	if !validChannel(channel) || strings.TrimSpace(currency) == "" {
		return ErrInvalidClearingInput
	}
	if (channel == ChannelWallet) != (walletBalanceAfter != nil) {
		return ErrInvalidClearingInput
	}

	gross := orderPayableCents(o)
	if gross < 0 {
		return ErrInvalidClearingInput
	}
	now := time.Now()
	record := &PaymentClearing{
		OrderID:     o.ID,
		BuyerID:     o.UserID,
		SellerID:    o.BossID,
		Channel:     channel,
		ProviderRef: strings.TrimSpace(providerRef),
		GrossCents:  gross,
		FeeCents:    0,
		NetCents:    gross,
		Currency:    strings.ToUpper(strings.TrimSpace(currency)),
		Status:      StatusCleared,
		ClearedAt:   now,
	}
	if err := tx.Create(record).Error; err != nil {
		return err
	}

	ledger := money.NewLedgerDaoByDB(tx)
	if walletBalanceAfter != nil {
		if err := ledger.AppendTransaction(
			o.UserID, o.ID, money.DirectionDebit, gross, *walletBalanceAfter, money.BizTypeOrderClear,
		); err != nil {
			return err
		}
	} else if err := ledger.AppendSystemTransaction(
		money.AccountCodeExternalClearing, o.ID, money.DirectionDebit, gross, money.BizTypeOrderClear,
	); err != nil {
		return err
	}
	return ledger.AppendSystemTransaction(
		money.AccountCodeMerchantEscrow, o.ID, money.DirectionCredit, gross, money.BizTypeOrderClear,
	)
}

// RecordExternalDuplicateTx 区分外部事件的“同一笔重放”和“确实重复收款”。
// 返回 matched=true 表示 provider_ref 与原清算单一致，可按幂等重放处理；否则持久化待人工退款异常。
func RecordExternalDuplicateTx(tx *gorm.DB, o *order.Order, channel, providerRef, providerAmount, currency string) (matched bool, err error) {
	providerRef = strings.TrimSpace(providerRef)
	if tx == nil || o == nil || o.ID == 0 || !isExternalChannel(channel) || providerRef == "" {
		return false, ErrInvalidClearingInput
	}

	var record PaymentClearing
	err = tx.Where("order_id = ?", o.ID).First(&record).Error
	if err == nil && record.Channel == channel && record.ProviderRef == providerRef {
		return true, nil
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return false, err
	}

	if err := RecordExternalAnomalyTx(tx, o, channel, providerRef, providerAmount, currency, AnomalyReasonDuplicatePayment); err != nil {
		return false, err
	}
	return false, nil
}

// RecordExternalAnomalyTx 持久化一笔已由可信外部事件证明实收、但不能进入正常清算的资金。
// 记录成功即表示异常已被系统接管；后续由运营/退款任务处理，不能再靠渠道重投修复。
func RecordExternalAnomalyTx(tx *gorm.DB, o *order.Order, channel, providerRef, providerAmount, currency, reason string) error {
	providerRef = strings.TrimSpace(providerRef)
	if tx == nil || o == nil || o.ID == 0 || !isExternalChannel(channel) || providerRef == "" {
		return ErrInvalidClearingInput
	}
	if reason != AnomalyReasonDuplicatePayment && reason != AnomalyReasonAmountMismatch &&
		reason != AnomalyReasonPaymentDetailsMismatch && reason != AnomalyReasonHighRiskPayment {
		return ErrInvalidClearingInput
	}
	anomaly := &PaymentAnomaly{
		OrderID:        o.ID,
		Channel:        channel,
		ProviderRef:    providerRef,
		ProviderAmount: strings.TrimSpace(providerAmount),
		Currency:       strings.ToUpper(strings.TrimSpace(currency)),
		Reason:         reason,
		Status:         AnomalyStatusPendingReview,
		OccurredAt:     time.Now(),
	}
	if anomaly.ProviderAmount == "" || anomaly.Currency == "" {
		return ErrInvalidClearingInput
	}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(anomaly).Error; err != nil {
		return err
	}
	return nil
}

// IsProviderCleared 在依赖 Redis 等一次性校验状态前，先用持久化清算单识别同一外部支付的重放。
// Web3 首次成功后会删除 pending；同 tx 重放仍应从 DB 幂等成功，不能因 pending 已删而无限重试。
func IsProviderCleared(ctx context.Context, orderID uint, channel, providerRef string) (bool, error) {
	return isProviderCleared(dao.NewDBClient(ctx), orderID, channel, providerRef)
}

func isProviderCleared(db *gorm.DB, orderID uint, channel, providerRef string) (bool, error) {
	providerRef = strings.TrimSpace(providerRef)
	if db == nil || orderID == 0 || providerRef == "" || !isExternalChannel(channel) {
		return false, ErrInvalidClearingInput
	}
	var count int64
	if err := db.Model(&PaymentClearing{}).
		Where("order_id=? AND channel=? AND provider_ref=?", orderID, channel, providerRef).
		Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

// SettleCompletedOrder 消费 order.completed 后执行真实结算。
// 对清算记录加行锁并检查状态；已经 settled/refunded 的重复事件直接成功返回。
func SettleCompletedOrder(ctx context.Context, orderID uint) error {
	if orderID == 0 {
		return ErrInvalidClearingInput
	}
	return settleCompletedOrder(dao.NewDBClient(ctx), orderID)
}

func settleCompletedOrder(db *gorm.DB, orderID uint) error {
	return db.Transaction(func(tx *gorm.DB) error {
		var o order.Order
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&o, orderID).Error; err != nil {
			return err
		}

		var record PaymentClearing
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("order_id = ?", orderID).First(&record).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				// 迁移前的普通订单在支付确认时已经直接给卖家入账，没有清算单，也不能再次放款。
				// completed 事件对这类订单只做兼容 no-op；历史账务继续由原 order_pay 等流水审计。
				return nil
			}
			return err
		}
		switch record.Status {
		case StatusSettled, StatusRefunded:
			return nil
		case StatusCleared:
			// 继续结算。
		default:
			return fmt.Errorf("%w: %s", ErrInvalidClearingState, record.Status)
		}
		// 退款中/已退款时，迟到的 completed 事件不能放款。退款若被驳回，会另发
		// order.refund_rejected，结算队列订阅该事件后再按 Completed 状态放款，避免这里热重试。
		if o.Type == consts.OrderRefunding || o.Type == consts.OrderRefunded {
			return nil
		}
		if o.Type != consts.OrderCompleted {
			return ErrOrderNotCompleted
		}
		if record.BuyerID != o.UserID || record.SellerID != o.BossID || record.NetCents < 0 {
			return ErrClearingOrderMismatch
		}

		seller, err := user.NewUserDaoByDB(tx).GetUserByIdForUpdate(record.SellerID)
		if err != nil {
			return err
		}
		before, err := seller.DecryptMoney()
		if err != nil {
			return err
		}
		after := before + record.NetCents
		// users.money 是业务查询余额时读取的余额快照。流水只负责审计，
		// 不会反向修改该字段，因此必须先把结算后的余额重新加密并显式落库。
		seller.Money = strconv.FormatInt(after, 10)
		cipher, err := seller.EncryptMoney()
		if err != nil {
			return err
		}
		seller.Money = cipher
		if err := tx.Model(&user.User{}).Where("id = ?", seller.ID).Update("money", cipher).Error; err != nil {
			return err
		}

		ledger := money.NewLedgerDaoByDB(tx)
		if err := ledger.AppendSystemTransaction(
			money.AccountCodeMerchantEscrow, orderID, money.DirectionDebit, record.NetCents, money.BizTypeOrderSettle,
		); err != nil {
			return err
		}
		if err := ledger.AppendTransaction(
			record.SellerID, orderID, money.DirectionCredit, record.NetCents, after, money.BizTypeOrderSettle,
		); err != nil {
			return err
		}

		now := time.Now()
		result := tx.Model(&PaymentClearing{}).
			Where("id = ? AND status = ?", record.ID, StatusCleared).
			Updates(map[string]interface{}{"status": StatusSettled, "settled_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrInvalidClearingState
		}
		return nil
	})
}

func orderPayableCents(o *order.Order) int64 {
	if o.PromoRuleID != 0 {
		return o.FinalCents
	}
	return o.Money * int64(o.Num)
}

func validChannel(channel string) bool {
	switch channel {
	case ChannelWallet, ChannelStripe, ChannelWeb3, ChannelXcash:
		return true
	default:
		return false
	}
}

func isExternalChannel(channel string) bool {
	switch channel {
	case ChannelStripe, ChannelWeb3, ChannelXcash:
		return true
	default:
		return false
	}
}
