//go:build exercise

package doubleentry

import "errors"

var (
	ErrInvalidAmount = errors.New("invalid amount")
	ErrSameAccount   = errors.New("buyer and seller must differ")
	ErrDuplicate     = errors.New("duplicate business key")
)

type Entry struct {
	BizKey    string
	AccountID uint
	Direction string
	Cents     int64
}

type Ledger struct {
	entries []Entry
	seen    map[string]struct{}
}

func NewLedger() *Ledger { return &Ledger{seen: map[string]struct{}{}} }

func (l *Ledger) Entries() []Entry { return append([]Entry(nil), l.entries...) }

func (l *Ledger) PostPayment(bizKey string, buyerID, sellerID uint, cents int64) error {
	// TODO: 校验支付并写入一对复式流水。
	// 1. cents <= 0 返回 ErrInvalidAmount；买卖双方相同返回 ErrSameAccount；
	// 2. bizKey 已存在于 seen 时返回 ErrDuplicate；
	// 3. 校验全部通过后，依次追加买家的 debit 和卖家的 credit，两条金额与 bizKey 相同；
	// 4. 最后把 bizKey 写入 seen。任何失败都不能留下流水或占用 bizKey。
	return nil
}
