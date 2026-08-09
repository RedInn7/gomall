//go:build exercise

package idempotencysm

import "testing"

func TestStateMachine(t *testing.T) {
	s := NewStore()
	a, _, _ := s.Begin("k")
	if a != Acquired {
		t.Fatal(a)
	}
	if err := s.CompleteSuccess("k", "ok"); err != nil {
		t.Fatal(err)
	}
	a, r, _ := s.Begin("k")
	if a != Replay || r != "ok" {
		t.Fatal(a, r)
	}
}
