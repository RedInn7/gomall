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

func (i *Inventory) Reserve(reservationID string, qty int) error {
	if reservationID == "" || qty <= 0 {
		return ErrInvalidQuantity
	}

	i.mu.Lock()
	defer i.mu.Unlock()

	if previous, ok := i.reservations[reservationID]; ok {
		if previous.Quantity != qty {
			return ErrReservationConflict
		}
		return nil
	}
	if i.available < qty {
		return ErrInsufficientStock
	}

	i.available -= qty
	i.reserved += qty
	i.reservations[reservationID] = Reservation{Quantity: qty, Status: StatusReserved}
	return nil
}

func (i *Inventory) Commit(reservationID string) error {
	i.mu.Lock()
	defer i.mu.Unlock()

	reservation, ok := i.reservations[reservationID]
	if !ok {
		return ErrUnknownReservation
	}
	switch reservation.Status {
	case StatusCommitted:
		return nil
	case StatusReleased:
		return ErrAlreadyFinalized
	case StatusReserved:
		i.reserved -= reservation.Quantity
		i.sold += reservation.Quantity
		reservation.Status = StatusCommitted
		i.reservations[reservationID] = reservation
		return nil
	default:
		return ErrAlreadyFinalized
	}
}

func (i *Inventory) Release(reservationID string) error {
	i.mu.Lock()
	defer i.mu.Unlock()

	reservation, ok := i.reservations[reservationID]
	if !ok {
		return ErrUnknownReservation
	}
	switch reservation.Status {
	case StatusReleased:
		return nil
	case StatusCommitted:
		return ErrAlreadyFinalized
	case StatusReserved:
		i.reserved -= reservation.Quantity
		i.available += reservation.Quantity
		reservation.Status = StatusReleased
		i.reservations[reservationID] = reservation
		return nil
	default:
		return ErrAlreadyFinalized
	}
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
