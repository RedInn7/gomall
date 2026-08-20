//go:build exercise

package atomicsettlement

import "errors"

const (
	StatusCleared   = "cleared"
	StatusSettled   = "settled"
	DirectionDebit  = "debit"
	DirectionCredit = "credit"
	AccountEscrow   = "merchant_escrow"
	AccountSeller   = "seller_wallet"
)

var (
	ErrInvalidInput       = errors.New("invalid settlement input")
	ErrInvalidState       = errors.New("invalid clearing state")
	ErrInsufficientEscrow = errors.New("insufficient escrow")
	ErrInjectedFailure    = errors.New("injected ledger failure")
)

type Clearing struct {
	OrderID, SellerID uint
	NetCents          int64
	Status            string
}
type Entry struct {
	OrderID, UserID                uint
	Account, Direction             string
	AmountCents, BalanceAfterCents int64
}
type Store struct {
	SellerBalances map[uint]int64
	EscrowBalance  int64
	Clearings      map[uint]Clearing
	Entries        []Entry
	FailAfterDebit bool
}

func NewStore(escrow int64, balances map[uint]int64, records ...Clearing) *Store {
	b := make(map[uint]int64, len(balances))
	for id, v := range balances {
		b[id] = v
	}
	c := make(map[uint]Clearing, len(records))
	for _, r := range records {
		c[r.OrderID] = r
	}
	return &Store{SellerBalances: b, EscrowBalance: escrow, Clearings: c}
}

// Settle 把托管资金原子地转入卖家余额，并留下金额相等的双边流水。
func (s *Store) Settle(orderID uint) error {
	// TODO 1：校验 store、orderID、清算单、SellerID 与正数 NetCents。
	// TODO 2：settled 重试应直接成功；只有 cleared 允许首次结算。
	// TODO 3：托管余额不足时拒绝，且所有表保持原样。
	// TODO 4：计算卖家新余额时检测 int64 溢出。
	// TODO 5：原子执行：托管扣款、卖家余额增加、写 debit/credit 两条流水、状态改 settled。
	// TODO 6：FailAfterDebit 模拟第一条流水后失败；失败时必须恢复余额、流水与清算状态。
	return nil
}
