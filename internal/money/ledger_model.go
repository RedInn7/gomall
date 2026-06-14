package money

import (
	"github.com/RedInn7/gomall/internal/shared/dbmodel"
)

// 资金流水方向：借记（账户余额减少）/ 贷记（账户余额增加）。
const (
	DirectionDebit  = "debit"
	DirectionCredit = "credit"
)

// 流水业务类型，用于对账时区分资金来源。
const (
	BizTypeOrderPay  = "order_pay"
	BizTypeStripePay = "stripe_pay"
	BizTypeWeb3Pay   = "web3_pay"
	// 出账 / 退款 / 两阶段：biz_type 进唯一索引，使同一订单可有多笔不同业务的流水
	// （如预售定金 + 尾款两次 debit），同业务同方向仍只入账一次。
	BizTypeRefund          = "refund"           // 退款获批：买家 credit / 卖家 debit
	BizTypePreorderDeposit = "preorder_deposit" // 预售定金
	BizTypePreorderFinal   = "preorder_final"   // 预售尾款
	BizTypePreorderRefund  = "preorder_refund"  // 预售定金退还
	BizTypeRedPacket       = "redpacket"        // 红包域汇总语义（保留兼容引用）
	BizTypeGroupBuy        = "groupbuy"         // 拼团支付 / 散团退款
	// 红包资金链拆三个子业务，避免共用 (ref_order_id,direction) 时跨阶段/跨领取人冲突：
	// 发包 ref=红包 id，退回 ref=红包 id，二者方向相同（escrow credit / debit）若同 biz_type 会撞唯一键，
	// 故分开；领包 ref=领取记录 id（每个领取人一行，天然唯一），与发/退的红包 id 空间隔离。
	BizTypeRedPacketSend   = "redpacket_send"   // 发包：发包人 debit / 平台清算 credit，ref=红包 id
	BizTypeRedPacketClaim  = "redpacket_claim"  // 领包：领取人 credit / 平台清算 debit，ref=领取记录 id
	BizTypeRedPacketRefund = "redpacket_refund" // 过期回收：发包人 credit / 平台清算 debit，ref=红包 id
)

// ExternalClearingUserID 平台对外资金清算账户的 user_id（0 = 系统账户）。
// Stripe / Web3 等外部资金入口，买家不从内部钱包扣款，复式记账的 debit 对手方记在该清算账户上，
// 保持 SUM(debit)=SUM(credit) 守恒。
const ExternalClearingUserID uint = 0

// StripeClearingUserID 保留兼容旧引用，语义同 ExternalClearingUserID。
const StripeClearingUserID = ExternalClearingUserID

// AccountTransaction 复式记账资金流水台账。
//
// 余额密文落库（users.money）不可对账，本表为每一次余额变动追加一条不可变流水：
// amount_cents / balance_after_cents 均为明文 int64 分，绝不加密，供对账与审计。
// 一次资金转移会成对出现（一方 debit、一方 credit），ref_order_id 关联订单。
//
// 幂等：对 (ref_order_id, direction, biz_type) 建唯一索引，保证同一订单同方向同业务只入账一次，
// 杜绝重复扣款 / 重复入账；biz_type 进键使两阶段业务（预售定金 + 尾款）可在同一订单各记一笔。
type AccountTransaction struct {
	dbmodel.Model
	UserID            uint   `gorm:"not null;index:idx_acct_tx_user"`
	Direction         string `gorm:"size:8;not null;uniqueIndex:uniq_acct_tx_order_dir,priority:2"`
	AmountCents       int64  `gorm:"not null"`
	RefOrderID        uint   `gorm:"not null;uniqueIndex:uniq_acct_tx_order_dir,priority:1"`
	BalanceAfterCents int64  `gorm:"not null"`
	BizType           string `gorm:"size:32;not null;uniqueIndex:uniq_acct_tx_order_dir,priority:3"`
}

func (AccountTransaction) TableName() string { return "account_transaction" }
