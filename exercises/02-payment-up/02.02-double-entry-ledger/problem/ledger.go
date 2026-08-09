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
	// TODO: 校验后原子地追加金额相等的 debit / credit；重复 bizKey 返回 ErrDuplicate。
	return nil
}
