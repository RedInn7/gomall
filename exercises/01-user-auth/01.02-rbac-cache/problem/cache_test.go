//go:build exercise

package rbaccache

import (
	"errors"
	"testing"
	"time"
)

func TestRoleCacheHitExpiryAndInvalidation(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	role := "merchant"
	loads := 0
	cache := NewRoleCache(30*time.Second, func(uint) (string, error) {
		loads++
		return role, nil
	})

	if got, err := cache.Lookup(7, now); err != nil || got != "merchant" {
		t.Fatalf("first lookup = %q, %v", got, err)
	}
	role = "admin"
	if got, _ := cache.Lookup(7, now.Add(29*time.Second)); got != "merchant" {
		t.Fatalf("cache hit = %q, want old role merchant", got)
	}
	if loads != 1 {
		t.Fatalf("loader calls = %d, want 1", loads)
	}

	if got, _ := cache.Lookup(7, now.Add(30*time.Second)); got != "admin" {
		t.Fatalf("expired lookup = %q, want admin", got)
	}
	cache.Invalidate(7)
	role = "customer"
	if got, _ := cache.Lookup(7, now.Add(31*time.Second)); got != "customer" {
		t.Fatalf("lookup after invalidation = %q, want customer", got)
	}
	if loads != 3 {
		t.Fatalf("loader calls = %d, want 3", loads)
	}
}

func TestRoleCacheDoesNotCacheLoaderError(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	loads := 0
	cache := NewRoleCache(30*time.Second, func(uint) (string, error) {
		loads++
		if loads == 1 {
			return "", errors.New("db unavailable")
		}
		return "customer", nil
	})
	if _, err := cache.Lookup(9, now); err == nil {
		t.Fatal("first lookup should fail")
	}
	if got, err := cache.Lookup(9, now); err != nil || got != "customer" {
		t.Fatalf("retry = %q, %v", got, err)
	}
	if loads != 2 {
		t.Fatalf("loader calls = %d, want 2", loads)
	}
}
