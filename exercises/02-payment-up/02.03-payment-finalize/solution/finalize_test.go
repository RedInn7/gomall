//go:build exercise

package paymentfinalize

import "testing"

func TestFinalizePayment(t *testing.T) {
	db := NewDB()
	db.Orders[1] = Order{ID: 1, State: WaitPay}
	if err := FinalizePayment(db, 1, "balance", "e1"); err != nil {
		t.Fatal(err)
	}
}
