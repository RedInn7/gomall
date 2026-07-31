//go:build exercise

package inventorybuckets

import "errors"

var (
	ErrInvalidQuantity     = errors.New("invalid quantity")
	ErrInsufficientStock   = errors.New("insufficient available stock")
	ErrInsufficientReserve = errors.New("insufficient reserved stock")
)

type Inventory struct {
	Available int
	Reserved  int
	Sold      int
}

func (i *Inventory) Reserve(qty int) error {
	// TODO
	return nil
}

func (i *Inventory) Commit(qty int) error {
	// TODO
	return nil
}

func (i *Inventory) Release(qty int) error {
	// TODO
	return nil
}
