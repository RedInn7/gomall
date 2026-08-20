//go:build exercise

package settlementrace

import (
	"errors"
	"sync"
)

const (
	IntentSettle = "settle"
	IntentRefund = "refund"

	StatusCleared  = "cleared"
	StatusSettled  = "settled"
	StatusRefunded = "refunded"
)

var (
	ErrInvalidInput     = errors.New("invalid settlement input")
	ErrTerminalConflict = errors.New("clearing already reached another terminal state")
	ErrLedgerWrite      = errors.New("ledger write failed")
)

type Clearing struct {
	OrderID, BuyerID, SellerID uint
	GrossCents, NetCents       int64
	Status                     string
}

type Entry struct {
	OrderID, UserID uint
	Account         string
	Direction       string
	Cents           int64
}

type Store struct {
	mu               sync.Mutex
	records          map[uint]Clearing
	sellerBalances   map[uint]int64
	buyerBalances    map[uint]int64
	entries          []Entry
	failSecondLedger bool
}

func NewStore(record Clearing, sellerBalance, buyerBalance int64) *Store {
	return &Store{
		records:        map[uint]Clearing{record.OrderID: record},
		sellerBalances: map[uint]int64{record.SellerID: sellerBalance},
		buyerBalances:  map[uint]int64{record.BuyerID: buyerBalance},
	}
}

func (s *Store) FailSecondLedgerWrite(fail bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failSecondLedger = fail
}
func (s *Store) Snapshot(orderID uint) (Clearing, int64, int64, []Entry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.records[orderID]
	return r, s.sellerBalances[r.SellerID], s.buyerBalances[r.BuyerID], append([]Entry(nil), s.entries...)
}

// Apply 处理订单完成后的结算和退款竞争。它必须可以被多个 goroutine 同时调用。
func Apply(store *Store, orderID uint, intent string) error {
	// TODO 1：校验 store、orderID 和 intent；锁必须覆盖“读状态—改余额—写双边流水—改状态”整个临界区。
	// TODO 2：cleared 才能首次处理。相同终态的重放返回 nil；相反终态返回 ErrTerminalConflict。
	// TODO 3：settle 将 NetCents 加到卖家余额，并写 escrow/debit 与 seller_wallet/credit。
	// TODO 4：refund 将 GrossCents 加到买家余额，并写 escrow/debit 与 buyer_wallet/credit。
	// TODO 5：两条流水金额必须相同；只有全部成功后才能推进为 settled/refunded。
	// TODO 6：第二条流水失败时，余额、流水和状态都必须恢复到调用前，返回 ErrLedgerWrite。
	// TODO 7：金额必须为正，更新买家或卖家余额前检查 int64 溢出。
	return nil
}
