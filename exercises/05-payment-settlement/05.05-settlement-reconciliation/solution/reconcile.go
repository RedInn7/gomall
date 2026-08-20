//go:build exercise

package settlementreconciliation

const (
	StatusSettled = "settled"
	EscrowAccount = "merchant_escrow"
	SellerAccount = "seller_wallet"
)

type Settlement struct {
	OrderID, SellerID uint
	NetCents          int64
	Channel, Status   string
}
type Entry struct {
	OrderID, UserID             uint
	Account, Direction, Channel string
	Cents                       int64
	BalanceBefore, BalanceAfter int64
}
type Issue struct {
	OrderID uint
	Code    string
}

func Reconcile(records []Settlement, entries []Entry) []Issue {
	byOrder := make(map[uint][]Entry, len(records))
	known := make(map[uint]bool, len(records))
	settled := make(map[uint]Settlement, len(records))
	duplicates := make(map[uint]bool)
	for _, r := range records {
		known[r.OrderID] = true
		if r.Status != StatusSettled {
			continue
		}
		if _, ok := settled[r.OrderID]; ok {
			duplicates[r.OrderID] = true
		} else {
			settled[r.OrderID] = r
		}
	}
	for _, e := range entries {
		byOrder[e.OrderID] = append(byOrder[e.OrderID], e)
	}
	issues := make([]Issue, 0)
	for id, r := range settled {
		code := ""
		es := byOrder[id]
		switch {
		case duplicates[id]:
			code = "duplicate_record"
		case len(es) != 2:
			code = "missing_entries"
		default:
			var debit, credit *Entry
			for i := range es {
				e := &es[i]
				if e.Account == EscrowAccount && e.Direction == "debit" {
					debit = e
				}
				if e.Account == SellerAccount && e.Direction == "credit" {
					credit = e
				}
			}
			switch {
			case debit == nil || credit == nil || debit.Cents != r.NetCents || credit.Cents != r.NetCents:
				code = "unbalanced"
			case credit.UserID != r.SellerID:
				code = "seller_mismatch"
			case debit.Channel != r.Channel || credit.Channel != r.Channel:
				code = "channel_mismatch"
			case credit.BalanceAfter-credit.BalanceBefore != r.NetCents:
				code = "balance_snapshot_mismatch"
			}
		}
		if code != "" {
			issues = append(issues, Issue{id, code})
		}
	}
	for id := range byOrder {
		if !known[id] {
			issues = append(issues, Issue{id, "orphan_entries"})
		}
	}
	radixSortIssues(issues)
	return issues
}

func radixSortIssues(a []Issue) {
	if len(a) < 2 {
		return
	}
	buf := make([]Issue, len(a))
	bits := 32 << (^uint(0) >> 63)
	for shift := 0; shift < bits; shift += 8 {
		var count [256]int
		for _, x := range a {
			count[byte(x.OrderID>>shift)]++
		}
		total := 0
		for i := range count {
			n := count[i]
			count[i] = total
			total += n
		}
		for _, x := range a {
			b := byte(x.OrderID >> shift)
			buf[count[b]] = x
			count[b]++
		}
		copy(a, buf)
	}
}
