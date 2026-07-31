//go:build exercise

package tokenversion

import (
	"errors"
	"time"
)

var (
	ErrBadSignature = errors.New("bad signature")
	ErrExpired      = errors.New("token expired")
	ErrWrongSubject = errors.New("wrong subject")
	ErrRevoked      = errors.New("token revoked")
	ErrForbidden    = errors.New("forbidden")
)

type Claims struct {
	Signed       bool
	UserID       uint
	Role         string
	TokenVersion uint64
	ExpiresAt    time.Time
}

type User struct {
	ID           uint
	Role         string
	TokenVersion uint64
}

func Authorize(claims Claims, user User, requiredRole string, now time.Time) error {
	if !claims.Signed {
		return ErrBadSignature
	}
	if !now.Before(claims.ExpiresAt) {
		return ErrExpired
	}
	if claims.UserID != user.ID {
		return ErrWrongSubject
	}
	if claims.TokenVersion != user.TokenVersion {
		return ErrRevoked
	}
	if user.Role != requiredRole {
		return ErrForbidden
	}
	return nil
}
