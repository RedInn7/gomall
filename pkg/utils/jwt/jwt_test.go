package jwt

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v4"

	conf "github.com/RedInn7/gomall/config"
)

func useTestSecret(t *testing.T) {
	t.Helper()
	prev := conf.Config
	conf.Config = &conf.Conf{EncryptSecret: &conf.EncryptSecret{JwtSecret: "jwt-test-secret"}}
	t.Cleanup(func() { conf.Config = prev })
}

// signAccess 直接签一枚 access token，用来构造过期 / 换算法的场景。
func signAccess(t *testing.T, method jwt.SigningMethod, id uint, expireAt time.Time) string {
	t.Helper()
	token, err := jwt.NewWithClaims(method, Claims{
		ID:       id,
		Username: "u",
		Type:     tokenTypeAccess,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expireAt),
			Issuer:    "mall",
		},
	}).SignedString(secret())
	if err != nil {
		t.Fatalf("sign access token: %v", err)
	}
	return token
}

func TestRefreshTokenIsBoundToUser(t *testing.T) {
	useTestSecret(t)
	_, refreshA, err := GenerateToken(1, "alice", 0)
	if err != nil {
		t.Fatal(err)
	}
	_, refreshB, err := GenerateToken(2, "bob", 0)
	if err != nil {
		t.Fatal(err)
	}
	expiredB := signAccess(t, jwt.SigningMethodHS256, 2, time.Now().Add(-time.Minute))

	if _, _, err := ParseRefreshToken(expiredB, refreshA); err == nil {
		t.Fatal("A 的 refresh token 配 B 的过期 access token 不能续签")
	}

	newAccess, _, err := ParseRefreshToken(expiredB, refreshB)
	if err != nil {
		t.Fatalf("B 用自己的 refresh token 续签失败: %v", err)
	}
	claims, err := ParseToken(newAccess)
	if err != nil || claims.ID != 2 {
		t.Fatalf("续签结果应属于 B: claims=%+v err=%v", claims, err)
	}
}

func TestTokenTypeIsEnforced(t *testing.T) {
	useTestSecret(t)
	_, refresh, err := GenerateToken(1, "alice", 0)
	if err != nil {
		t.Fatal(err)
	}
	emailToken, err := GenerateEmailToken(1, 1, "a@example.com", "")
	if err != nil {
		t.Fatal(err)
	}

	for name, token := range map[string]string{
		"邮件 token":      emailToken,
		"refresh token": refresh,
		"HS512 签名":      signAccess(t, jwt.SigningMethodHS512, 1, time.Now().Add(time.Hour)),
	} {
		if _, err := ParseToken(token); err == nil {
			t.Fatalf("%s 不能当 access token 用", name)
		}
		if _, _, err := ParseRefreshToken(token, refresh); err == nil {
			t.Fatalf("%s 不能通过鉴权中间件的续签入口", name)
		}
	}

	access, _, err := GenerateToken(1, "alice", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseEmailToken(access); err == nil {
		t.Fatal("access token 不能当邮件 token 用")
	}
	if _, err := ParseEmailToken(emailToken); err != nil {
		t.Fatalf("合法邮件 token 解析失败: %v", err)
	}
}
