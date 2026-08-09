//go:build exercise

package reconciliation

import "testing"

func TestHealthy(t *testing.T) {
	o := []Order{{ID: 1, State: "paid", Channel: "balance", PaidCents: 1}}
	e := []Entry{{1, "debit", "balance", 1}, {1, "credit", "balance", 1}}
	if got := Reconcile(o, e); len(got) != 0 {
		t.Fatal(got)
	}
}
