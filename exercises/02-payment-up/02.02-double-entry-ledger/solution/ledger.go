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

func NewLedger() *Ledger           { return &Ledger{seen: map[string]struct{}{}} }
func (l *Ledger) Entries() []Entry { return append([]Entry(nil), l.entries...) }

func (l *Ledger) PostPayment(bizKey string, buyerID, sellerID uint, cents int64) error {
	if cents <= 0 {
		return ErrInvalidAmount
	}
	if buyerID == sellerID {
		return ErrSameAccount
	}
	if _, ok := l.seen[bizKey]; ok {
		return ErrDuplicate
	}
	l.entries = append(l.entries,
		Entry{BizKey: bizKey, AccountID: buyerID, Direction: "debit", Cents: cents},
		Entry{BizKey: bizKey, AccountID: sellerID, Direction: "credit", Cents: cents},
	)
	l.seen[bizKey] = struct{}{}
	return nil
}
