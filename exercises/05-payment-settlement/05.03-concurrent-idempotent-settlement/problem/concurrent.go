//go:build exercise

package concurrentsettlement

import (
	"errors"
	"sync"
)

const (
	StatusCleared = "cleared"
	StatusSettled = "settled"
)

var (
	ErrUnknownOrder = errors.New("unknown order")
	ErrInvalidState = errors.New("invalid state")
)

type Clearing struct {
	OrderID, SellerID uint
	Amount            int64
	Status            string
}
type Entry struct {
	OrderID, SellerID uint
	Amount            int64
}
type Snapshot struct {
	Escrow         int64
	SellerBalances map[uint]int64
	Clearings      map[uint]Clearing
	Entries        []Entry
}
type Store struct {
	mu        sync.Mutex
	escrow    int64
	sellers   map[uint]int64
	clearings map[uint]Clearing
	entries   []Entry
}

func NewStore(escrow int64, records ...Clearing) *Store {
	c := make(map[uint]Clearing, len(records))
	for _, r := range records {
		c[r.OrderID] = r
	}
	return &Store{escrow: escrow, sellers: map[uint]int64{}, clearings: c}
}

// Settle 必须能承受同一订单的并发重复投递，以及不同订单同时结算到同一卖家。
func (s *Store) Settle(orderID uint) error {
	// TODO 1：通过互斥同步保护“读取状态—检查—修改余额—写流水—推进状态”整个临界区。
	// TODO 2：不存在的订单返回 ErrUnknownOrder；settled 重试幂等成功。
	// TODO 3：只有 cleared、正数金额和有效 SellerID 可以结算，否则返回 ErrInvalidState。
	// TODO 4：余额不足返回 ErrInvalidState，且不允许产生部分写入。
	// TODO 5：一次成功结算只能追加一条卖家入账事件并将状态推进为 settled。
	// TODO 6：不要只锁 map 写入；检查与写入分开会产生 TOCTOU，导致重复放款。
	return nil
}

func (s *Store) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := make(map[uint]int64, len(s.sellers))
	for k, v := range s.sellers {
		b[k] = v
	}
	c := make(map[uint]Clearing, len(s.clearings))
	for k, v := range s.clearings {
		c[k] = v
	}
	return Snapshot{s.escrow, b, c, append([]Entry(nil), s.entries...)}
}
