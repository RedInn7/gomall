//go:build exercise

package settlementreconciliation

import (
	"reflect"
	"testing"
)

func pair(id, seller uint, cents, before int64, channel string) []Entry {
	return []Entry{
		{OrderID: id, Account: EscrowAccount, Direction: "debit", Cents: cents, Channel: channel},
		{OrderID: id, UserID: seller, Account: SellerAccount, Direction: "credit", Cents: cents, Channel: channel, BalanceBefore: before, BalanceAfter: before + cents},
	}
}

func TestReconcileReportsAllClassesInOrder(t *testing.T) {
	records := []Settlement{
		{OrderID: 50, SellerID: 5, NetCents: 500, Channel: "wallet", Status: StatusSettled},
		{OrderID: 10, SellerID: 1, NetCents: 100, Channel: "stripe", Status: StatusSettled},
		{OrderID: 40, SellerID: 4, NetCents: 400, Channel: "web3", Status: StatusSettled},
		{OrderID: 20, SellerID: 2, NetCents: 200, Channel: "stripe", Status: StatusSettled},
		{OrderID: 30, SellerID: 3, NetCents: 300, Channel: "wallet", Status: StatusSettled},
	}
	entries := append(pair(10, 1, 100, 900, "stripe"), Entry{OrderID: 20, Account: EscrowAccount, Direction: "debit", Cents: 200, Channel: "stripe"})
	entries = append(entries, pair(30, 3, 299, 700, "wallet")...)
	entries = append(entries, pair(40, 99, 400, 600, "web3")...)
	entries = append(entries, pair(50, 5, 500, 100, "stripe")...)
	want := []Issue{{20, "missing_entries"}, {30, "unbalanced"}, {40, "seller_mismatch"}, {50, "channel_mismatch"}}
	if got := Reconcile(records, entries); !reflect.DeepEqual(got, want) {
		t.Fatalf("got=%+v want=%+v", got, want)
	}
}

func TestBalanceSnapshotAndPriority(t *testing.T) {
	records := []Settlement{{OrderID: 7, SellerID: 8, NetCents: 90, Channel: "wallet", Status: StatusSettled}, {OrderID: 8, SellerID: 8, NetCents: 90, Channel: "wallet", Status: StatusSettled}}
	entries := pair(7, 8, 90, 100, "wallet")
	entries[1].BalanceAfter = 180
	entries = append(entries, Entry{OrderID: 8, Account: EscrowAccount, Direction: "debit", Cents: 1, Channel: "wrong"})
	want := []Issue{{7, "balance_snapshot_mismatch"}, {8, "missing_entries"}}
	if got := Reconcile(records, entries); !reflect.DeepEqual(got, want) {
		t.Fatalf("got=%+v want=%+v", got, want)
	}
}

func TestDuplicateRecordAndOrphanEntries(t *testing.T) {
	records := []Settlement{{OrderID: 3, SellerID: 1, NetCents: 10, Channel: "wallet", Status: StatusSettled}, {OrderID: 3, SellerID: 1, NetCents: 10, Channel: "wallet", Status: StatusSettled}, {OrderID: 2, Status: "cleared"}}
	entries := append(pair(3, 1, 10, 0, "wallet"), pair(99, 7, 20, 0, "wallet")...)
	want := []Issue{{3, "duplicate_record"}, {99, "orphan_entries"}}
	if got := Reconcile(records, entries); !reflect.DeepEqual(got, want) {
		t.Fatalf("got=%+v want=%+v", got, want)
	}
}

func TestValidRecordsAndNonSettledRecordsAreIgnored(t *testing.T) {
	records := []Settlement{{OrderID: 1, SellerID: 2, NetCents: 30, Channel: "web3", Status: StatusSettled}, {OrderID: 2, SellerID: 2, NetCents: 40, Channel: "web3", Status: "cleared"}}
	if got := Reconcile(records, pair(1, 2, 30, 100, "web3")); len(got) != 0 {
		t.Fatalf("got=%+v", got)
	}
}

func TestRadixSortHandlesHighUintBits(t *testing.T) {
	high := uint(^uint(0) - 1)
	issues := []Issue{{high, "x"}, {256, "x"}, {1, "x"}, {65536, "x"}, {0, "x"}}
	radixSortIssues(issues)
	want := []Issue{{0, "x"}, {1, "x"}, {256, "x"}, {65536, "x"}, {high, "x"}}
	if !reflect.DeepEqual(issues, want) {
		t.Fatalf("got=%+v", issues)
	}
}

func TestLargeBatch(t *testing.T) {
	const n = 20000
	records := make([]Settlement, 0, n)
	entries := make([]Entry, 0, 2*n)
	for i := n; i >= 1; i-- {
		id := uint(i)
		records = append(records, Settlement{OrderID: id, SellerID: id + 1, NetCents: int64(i), Channel: "wallet", Status: StatusSettled})
		entries = append(entries, pair(id, id+1, int64(i), 100, "wallet")...)
	}
	entries[len(entries)-1].Cents++
	if got := Reconcile(records, entries); !reflect.DeepEqual(got, []Issue{{1, "unbalanced"}}) {
		t.Fatalf("got=%+v", got)
	}
}
