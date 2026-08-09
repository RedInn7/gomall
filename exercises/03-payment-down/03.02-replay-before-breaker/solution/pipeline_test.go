//go:build exercise

package replaybreaker

import "testing"

func TestReplay(t *testing.T) {
	c := NewCache()
	c.Seed("k", "ok")
	got, err := Handle(c, &Breaker{Open: true}, "k", func() (string, error) { panic("called") })
	if err != nil || got != "ok" {
		t.Fatal(got, err)
	}
}
