//go:build exercise

package tokenversion

import (
	"errors"
	"testing"
	"time"
)

func TestAuthorizeUsesCurrentIdentityVersionAndRole(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	user := User{ID: 7, Role: "merchant", TokenVersion: 4}
	valid := Claims{
		Signed: true, UserID: 7, Role: "admin",
		TokenVersion: 4, ExpiresAt: now.Add(time.Minute),
	}

	if err := Authorize(valid, user, "merchant", now); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}
	if err := Authorize(valid, user, "admin", now); !errors.Is(err, ErrForbidden) {
		t.Fatalf("forged role error = %v, want %v", err, ErrForbidden)
	}

	revoked := valid
	revoked.TokenVersion = 3
	if err := Authorize(revoked, user, "merchant", now); !errors.Is(err, ErrRevoked) {
		t.Fatalf("old token error = %v, want %v", err, ErrRevoked)
	}
}

func TestAuthorizeRejectsInvalidAuthentication(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	user := User{ID: 7, Role: "customer", TokenVersion: 2}
	base := Claims{Signed: true, UserID: 7, TokenVersion: 2, ExpiresAt: now.Add(time.Minute)}

	tests := []struct {
		name    string
		mutate  func(*Claims)
		wantErr error
	}{
		{"bad signature", func(c *Claims) { c.Signed = false }, ErrBadSignature},
		{"expired at boundary", func(c *Claims) { c.ExpiresAt = now }, ErrExpired},
		{"wrong user", func(c *Claims) { c.UserID = 8 }, ErrWrongSubject},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claims := base
			tt.mutate(&claims)
			if err := Authorize(claims, user, "customer", now); !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}
