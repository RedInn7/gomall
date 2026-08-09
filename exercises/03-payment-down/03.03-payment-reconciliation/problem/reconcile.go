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
	// TODO: 只核对 paid 订单，按题面优先级报告一条异常，并按 OrderID 升序返回。
	return nil
}

func sortIssues(issues []Issue) {
	sort.Slice(issues, func(i, j int) bool { return issues[i].OrderID < issues[j].OrderID })
}
