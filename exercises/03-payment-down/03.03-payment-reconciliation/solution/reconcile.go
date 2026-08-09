//go:build exercise

package reconciliation

import "sort"

type Order struct {
	ID             uint
	State, Channel string
	PaidCents      int64
}
type Entry struct {
	OrderID            uint
	Direction, Channel string
	Cents              int64
}
type Issue struct {
	OrderID uint
	Code    string
}

func Reconcile(orders []Order, entries []Entry) []Issue {
	byOrder := map[uint][]Entry{}
	for _, e := range entries {
		byOrder[e.OrderID] = append(byOrder[e.OrderID], e)
	}
	issues := []Issue{}
	for _, o := range orders {
		if o.State != "paid" {
			continue
		}
		es := byOrder[o.ID]
		code := ""
		if len(es) != 2 {
			code = "missing_entries"
		} else {
			debits, credits := 0, 0
			balanced := true
			channelOK := true
			for _, e := range es {
				if e.Direction == "debit" {
					debits++
				}
				if e.Direction == "credit" {
					credits++
				}
				if e.Cents != o.PaidCents {
					balanced = false
				}
				if e.Channel != o.Channel {
					channelOK = false
				}
			}
			if debits != 1 || credits != 1 || !balanced {
				code = "unbalanced"
			} else if !channelOK {
				code = "channel_mismatch"
			}
		}
		if code != "" {
			issues = append(issues, Issue{OrderID: o.ID, Code: code})
		}
	}
	sortIssues(issues)
	return issues
}
func sortIssues(issues []Issue) {
	sort.Slice(issues, func(i, j int) bool { return issues[i].OrderID < issues[j].OrderID })
}
