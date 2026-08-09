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
	// TODO: 按以下顺序完成鉴权，命中一项就立即返回对应错误：
	// 1. claims.Signed 为 false：ErrBadSignature；
	// 2. now 不早于 claims.ExpiresAt：ErrExpired；
	// 3. claims.UserID 与 user.ID 不同：ErrWrongSubject；
	// 4. claims.TokenVersion 与 user.TokenVersion 不同：ErrRevoked；
	// 5. user.Role 与 requiredRole 不同：ErrForbidden。
	// 角色必须读取当前 user，不能相信 token 中可能过时或伪造的 claims.Role。
	return nil
}
