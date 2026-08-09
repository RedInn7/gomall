//go:build exercise

package doubleentry

import "testing"

func TestPostPayment(t *testing.T) {
	l := NewLedger()
	if err := l.PostPayment("order:1", 1, 2, 100); err != nil {
		t.Fatal(err)
	}
	if len(l.Entries()) != 2 {
		t.Fatalf("entries = %d", len(l.Entries()))
	}
}
