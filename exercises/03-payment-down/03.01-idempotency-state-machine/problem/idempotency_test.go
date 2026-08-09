//go:build exercise

package idempotencysm

import (
	"errors"
	"testing"
)

func TestPaymentIntentMovesFromAcquiredToWaitToReplay(t *testing.T) {
	s := NewStore()
	if action, _, err := s.Begin("pay-42"); err != nil || action != Acquired {
		t.Fatalf("first begin = %s, %v", action, err)
	}
	if action, _, err := s.Begin("pay-42"); err != nil || action != Wait {
		t.Fatalf("second begin = %s, %v", action, err)
	}
	if err := s.CompleteSuccess("pay-42", `{"status":"paid"}`); err != nil {
		t.Fatal(err)
	}
	if action, response, err := s.Begin("pay-42"); err != nil || action != Replay || response != `{"status":"paid"}` {
		t.Fatalf("replay = %s %q, %v", action, response, err)
	}
}

func TestCompleteSuccessRequiresProcessingRecord(t *testing.T) {
	s := NewStore()
	if err := s.CompleteSuccess("missing", "ok"); !errors.Is(err, ErrNotProcessing) {
		t.Fatalf("error = %v", err)
	}
	_, _, _ = s.Begin("pay-42")
	_ = s.CompleteSuccess("pay-42", "first")
	if err := s.CompleteSuccess("pay-42", "second"); !errors.Is(err, ErrAlreadyDone) {
		t.Fatalf("error = %v", err)
	}
	_, got, _ := s.Begin("pay-42")
	if got != "first" {
		t.Fatalf("response overwritten: %q", got)
	}
}
