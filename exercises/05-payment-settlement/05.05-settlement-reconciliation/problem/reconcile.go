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
	Channel           string
	Status            string
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

// Reconcile 对 settled 清算单与结算流水做批量对账。
func Reconcile(records []Settlement, entries []Entry) []Issue {
	// TODO 1：一次扫描 entries，按 OrderID 建立索引；不要为每张清算单重新遍历全部流水。
	// TODO 2：只核对 StatusSettled。每张单必须恰好有两条流水，否则 missing_entries。
	// TODO 3：必须是 merchant_escrow/debit 与 seller_wallet/credit，且两边金额都等于 NetCents；否则 unbalanced。
	// TODO 4：卖家流水的 UserID 必须等于 SellerID，否则 seller_mismatch。
	// TODO 5：两条流水的 Channel 必须等于清算单 Channel，否则 channel_mismatch。
	// TODO 6：卖家流水须满足 BalanceAfter-BalanceBefore==NetCents，否则 balance_snapshot_mismatch。
	// TODO 7：同一 settled OrderID 出现多张清算单时返回 duplicate_record，并避免重复报告。
	// TODO 8：流水找不到任何清算单时返回 orphan_entries。每个订单只保留优先级最高的问题。
	// TODO 9：结果按 OrderID 升序。主体索引和核对为 O(n+m)，排序使用线性时间的 uint 基数排序。
	return nil
}

func radixSortIssues(issues []Issue) {
	// TODO：按 uint 的每个字节做稳定计数排序，不得调用 sort.Slice。
}
