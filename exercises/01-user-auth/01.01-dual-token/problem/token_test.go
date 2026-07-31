//go:build exercise

package dualtoken

import (
	"testing"
	"time"
)

func TestNextAction(t *testing.T) {
	base := time.Unix(1_800_000_000, 0)
	tests := []struct {
		name    string
		now     time.Time
		access  time.Time
		refresh time.Time
		want    Action
	}{
		{"access valid", base, base.Add(time.Minute), base.Add(24 * time.Hour), Pass},
		{"access expired", base, base.Add(-time.Second), base.Add(time.Hour), Refresh},
		{"access expires exactly now", base, base, base.Add(time.Hour), Refresh},
		{"both expired", base, base.Add(-time.Hour), base.Add(-time.Second), Relogin},
		{"refresh expires exactly now", base, base.Add(-time.Second), base, Relogin},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NextAction(tt.now, tt.access, tt.refresh); got != tt.want {
				t.Fatalf("NextAction() = %q, want %q", got, tt.want)
			}
		})
	}
}
