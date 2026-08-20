//go:build exercise

package settlementreconciliation

import (
	"reflect"
	"testing"
)

func pair(id, seller uint, cents, before int64, ch string) []Entry {
	return []Entry{{OrderID: id, Account: EscrowAccount, Direction: "debit", Cents: cents, Channel: ch}, {OrderID: id, UserID: seller, Account: SellerAccount, Direction: "credit", Cents: cents, Channel: ch, BalanceBefore: before, BalanceAfter: before + cents}}
}
func TestReconcileReportsAllClassesInOrder(t *testing.T) {
	r := []Settlement{{50, 5, 500, "wallet", StatusSettled}, {10, 1, 100, "stripe", StatusSettled}, {40, 4, 400, "web3", StatusSettled}, {20, 2, 200, "stripe", StatusSettled}, {30, 3, 300, "wallet", StatusSettled}}
	e := append(pair(10, 1, 100, 900, "stripe"), Entry{OrderID: 20, Account: EscrowAccount, Direction: "debit", Cents: 200, Channel: "stripe"})
	e = append(e, pair(30, 3, 299, 700, "wallet")...)
	e = append(e, pair(40, 99, 400, 600, "web3")...)
	e = append(e, pair(50, 5, 500, 100, "stripe")...)
	w := []Issue{{20, "missing_entries"}, {30, "unbalanced"}, {40, "seller_mismatch"}, {50, "channel_mismatch"}}
	if g := Reconcile(r, e); !reflect.DeepEqual(g, w) {
		t.Fatal(g)
	}
}
func TestBalanceSnapshotAndPriority(t *testing.T) {
	r := []Settlement{{7, 8, 90, "wallet", StatusSettled}, {8, 8, 90, "wallet", StatusSettled}}
	e := pair(7, 8, 90, 100, "wallet")
	e[1].BalanceAfter = 180
	e = append(e, Entry{OrderID: 8, Account: EscrowAccount, Direction: "debit", Cents: 1, Channel: "wrong"})
	w := []Issue{{7, "balance_snapshot_mismatch"}, {8, "missing_entries"}}
	if g := Reconcile(r, e); !reflect.DeepEqual(g, w) {
		t.Fatal(g)
	}
}
func TestDuplicateRecordAndOrphanEntries(t *testing.T) {
	r := []Settlement{{3, 1, 10, "wallet", StatusSettled}, {3, 1, 10, "wallet", StatusSettled}, {OrderID: 2, Status: "cleared"}}
	e := append(pair(3, 1, 10, 0, "wallet"), pair(99, 7, 20, 0, "wallet")...)
	w := []Issue{{3, "duplicate_record"}, {99, "orphan_entries"}}
	if g := Reconcile(r, e); !reflect.DeepEqual(g, w) {
		t.Fatal(g)
	}
}
func TestValidRecordsAndNonSettledRecordsAreIgnored(t *testing.T) {
	r := []Settlement{{1, 2, 30, "web3", StatusSettled}, {2, 2, 40, "web3", "cleared"}}
	if g := Reconcile(r, pair(1, 2, 30, 100, "web3")); len(g) != 0 {
		t.Fatal(g)
	}
}
func TestRadixSortHandlesHighUintBits(t *testing.T) {
	h := uint(^uint(0) - 1)
	a := []Issue{{h, "x"}, {256, "x"}, {1, "x"}, {65536, "x"}, {0, "x"}}
	radixSortIssues(a)
	w := []Issue{{0, "x"}, {1, "x"}, {256, "x"}, {65536, "x"}, {h, "x"}}
	if !reflect.DeepEqual(a, w) {
		t.Fatal(a)
	}
}
func TestLargeBatch(t *testing.T) {
	const n = 20000
	r := make([]Settlement, 0, n)
	e := make([]Entry, 0, 2*n)
	for i := n; i >= 1; i-- {
		id := uint(i)
		r = append(r, Settlement{id, id + 1, int64(i), "wallet", StatusSettled})
		e = append(e, pair(id, id+1, int64(i), 100, "wallet")...)
	}
	e[len(e)-1].Cents++
	if g := Reconcile(r, e); !reflect.DeepEqual(g, []Issue{{1, "unbalanced"}}) {
		t.Fatal(g)
	}
}
