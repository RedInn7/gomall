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
	// TODO: 把 qty 件库存从 Available 转入 Reserved。
	// qty <= 0 返回 ErrInvalidQuantity；Available 不足返回 ErrInsufficientStock。
	// 失败时三个库存桶都不能变化。
	return nil
}

func (i *Inventory) Commit(qty int) error {
	// TODO: 支付成功后，把 qty 件库存从 Reserved 转入 Sold。
	// qty <= 0 返回 ErrInvalidQuantity；Reserved 不足返回 ErrInsufficientReserve。
	// 失败时三个库存桶都不能变化。
	return nil
}

func (i *Inventory) Release(qty int) error {
	// TODO: 取消订单时，把 qty 件库存从 Reserved 退回 Available。
	// qty <= 0 返回 ErrInvalidQuantity；Reserved 不足返回 ErrInsufficientReserve。
	// 失败时三个库存桶都不能变化。
	return nil
}
