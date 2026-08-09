//go:build exercise

package replaybreaker

import (
	"errors"
	"testing"
)

func TestCompletedPaymentReplaysEvenWhenCircuitIsOpen(t *testing.T) {
	cache := NewCache()
	cache.Seed("pay-1", "paid")
	breaker := &Breaker{Open: true}
	calls := 0
	got, err := Handle(cache, breaker, "pay-1", func() (string, error) { calls++; return "", nil })
	if err != nil || got != "paid" || calls != 0 {
		t.Fatalf("got=%q err=%v calls=%d", got, err, calls)
	}
}
func TestOpenCircuitRejectsNewPayment(t *testing.T) {
	cache := NewCache()
	breaker := &Breaker{Open: true}
	calls := 0
	_, err := Handle(cache, breaker, "pay-2", func() (string, error) { calls++; return "paid", nil })
	if !errors.Is(err, ErrCircuitOpen) || calls != 0 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}
func TestOnlySystemErrorsCountTowardBreaker(t *testing.T) {
	cache := NewCache()
	breaker := &Breaker{}
	_, _ = Handle(cache, breaker, "business", func() (string, error) { return "", &PaymentError{Kind: BusinessError, Message: "insufficient balance"} })
	if breaker.Failures != 0 {
		t.Fatalf("business failure counted: %d", breaker.Failures)
	}
	_, _ = Handle(cache, breaker, "system", func() (string, error) { return "", &PaymentError{Kind: SystemError, Message: "db timeout"} })
	if breaker.Failures != 1 {
		t.Fatalf("system failures = %d, want 1", breaker.Failures)
	}
}
func TestSuccessIsCachedForRetry(t *testing.T) {
	cache := NewCache()
	breaker := &Breaker{}
	calls := 0
	pay := func() (string, error) { calls++; return "paid-42", nil }
	if _, err := Handle(cache, breaker, "pay-42", pay); err != nil {
		t.Fatal(err)
	}
	got, err := Handle(cache, breaker, "pay-42", pay)
	if err != nil || got != "paid-42" || calls != 1 {
		t.Fatalf("got=%q err=%v calls=%d", got, err, calls)
	}
}
