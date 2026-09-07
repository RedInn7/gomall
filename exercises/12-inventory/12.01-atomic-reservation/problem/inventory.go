//go:build exercise

package atomicreservation

import (
	"errors"
	"sync"
)

var (
	ErrInvalidQuantity     = errors.New("invalid quantity")
	ErrInsufficientStock   = errors.New("insufficient available stock")
	ErrUnknownReservation  = errors.New("unknown reservation")
	ErrReservationConflict = errors.New("reservation conflict")
	ErrAlreadyFinalized    = errors.New("reservation already finalized")
)

const (
	StatusReserved  = "reserved"
	StatusCommitted = "committed"
	StatusReleased  = "released"
)

type Reservation struct {
	Quantity int
	Status   string
}

type Snapshot struct {
	Available    int
	Reserved     int
	Sold         int
	Reservations map[string]Reservation
}

type Inventory struct {
	mu           sync.Mutex
	available    int
	reserved     int
	sold         int
	reservations map[string]Reservation
}

func NewInventory(available int) *Inventory {
	return &Inventory{
		available:    available,
		reservations: make(map[string]Reservation),
	}
}

// Reserve 原子地把 qty 件库存从 Available 移到 Reserved，并用 reservationID
// 记录这次预占。相同 ID、相同数量的重试必须幂等成功。
func (i *Inventory) Reserve(reservationID string, qty int) error {
	// TODO 1：校验 reservationID 和 qty，非法输入返回 ErrInvalidQuantity。
	// TODO 2：锁住“查询幂等记录—检查库存—移动库存—写入记录”整个过程。
	// TODO 3：相同 ID、相同数量的 Reserve 重试返回 nil，不能重复扣库存。
	// TODO 4：相同 ID、不同数量返回 ErrReservationConflict，且不能修改状态。
	// TODO 5：库存不足返回 ErrInsufficientStock，且不能留下 reservation 记录。
	return nil
}

// Commit 在支付成功后把这次预占转为已售。重复 Commit 必须幂等成功。
func (i *Inventory) Commit(reservationID string) error {
	// TODO 1：在同一临界区内读取 reservation、校验状态并移动库存。
	// TODO 2：未知 ID 返回 ErrUnknownReservation；重复 Commit 返回 nil。
	// TODO 3：已经 Release 的预占不能 Commit，返回 ErrAlreadyFinalized。
	// TODO 4：成功时 Reserved 减少、Sold 增加，Available 不变。
	return nil
}

// Release 在订单取消或建单失败时把预占库存退回可售。重复 Release 必须幂等成功。
func (i *Inventory) Release(reservationID string) error {
	// TODO 1：在同一临界区内读取 reservation、校验状态并移动库存。
	// TODO 2：未知 ID 返回 ErrUnknownReservation；重复 Release 返回 nil。
	// TODO 3：已经 Commit 的预占不能 Release，返回 ErrAlreadyFinalized。
	// TODO 4：成功时 Reserved 减少、Available 增加，Sold 不变。
	return nil
}

func (i *Inventory) Snapshot() Snapshot {
	i.mu.Lock()
	defer i.mu.Unlock()
	records := make(map[string]Reservation, len(i.reservations))
	for id, reservation := range i.reservations {
		records[id] = reservation
	}
	return Snapshot{
		Available:    i.available,
		Reserved:     i.reserved,
		Sold:         i.sold,
		Reservations: records,
	}
}
