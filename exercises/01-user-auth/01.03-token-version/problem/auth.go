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
	// TODO
	return nil
}
