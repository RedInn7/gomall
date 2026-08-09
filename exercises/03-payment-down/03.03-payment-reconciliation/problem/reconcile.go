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
	// TODO: 对每个 State == "paid" 的订单核对流水，其他订单跳过。
	// 1. 流水数量不是 2：missing_entries；
	// 2. 数量正确但不是一条 debit 加一条 credit，或任一金额不等于 PaidCents：unbalanced；
	// 3. 金额与方向正确，但任一流水渠道不等于订单渠道：channel_mismatch；
	// 4. 每个订单最多返回一个 Issue，优先级按上面的顺序，最后调用 sortIssues 按 OrderID 升序排列。
	return nil
}

func sortIssues(issues []Issue) {
	sort.Slice(issues, func(i, j int) bool { return issues[i].OrderID < issues[j].OrderID })
}
