package jwt

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v4"

	conf "github.com/RedInn7/gomall/config"
	"github.com/RedInn7/gomall/consts"
	utilLog "github.com/RedInn7/gomall/pkg/utils/log"
)

func init() {
	// 初始化测试用 config，避免依赖外部文件
	conf.Config = &conf.Conf{
		EncryptSecret: &conf.EncryptSecret{
			JwtSecret: "test-secret-key-for-unit-tests",
		},
	}
	// 初始化 logger，避免 ParseRefreshToken 中 log.LogrusObj nil 指针崩溃
	utilLog.InitLog()
}

// makeTokenWithExpiry 签发自定义过期时间的 access token，用于测试过期场景。
func makeTokenWithExpiry(id uint, username string, expireAt time.Time) (string, error) {
	claims := Claims{
		ID:       id,
		Username: username,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expireAt),
			Issuer:    "mall",
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(secret())
}

// TestParseRefreshToken_ExpiredAccessValidRefresh 验证 access token 已过期但 refresh token 有效时能成功续签。
// 这是 BUG 修复的核心回归测试：旧代码因为 ParseToken 返回 error 就直接 return，
// 使得 access 过期时刷新路径永远不可达。
func TestParseRefreshToken_ExpiredAccessValidRefresh(t *testing.T) {
	// 签发一个已经过期 1 秒的 access token
	expiredAccessToken, err := makeTokenWithExpiry(42, "alice", time.Now().Add(-time.Second))
	if err != nil {
		t.Fatalf("makeTokenWithExpiry: %v", err)
	}

	// 签发一个仍然有效的 refresh token（10天有效期）
	_, refreshToken, err := GenerateToken(42, "alice", 0)
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}

	// 调用 ParseRefreshToken 应成功返回新的 token 对，而非错误
	newAccess, newRefresh, err := ParseRefreshToken(expiredAccessToken, refreshToken)
	if err != nil {
		t.Fatalf("ParseRefreshToken returned error on valid refresh: %v", err)
	}
	if newAccess == "" || newRefresh == "" {
		t.Fatal("ParseRefreshToken returned empty tokens")
	}

	// 新签发的 access token 应该能够正常解析
	claims, err := ParseToken(newAccess)
	if err != nil {
		t.Fatalf("new access token is invalid: %v", err)
	}
	if claims.ID != 42 || claims.Username != "alice" {
		t.Errorf("claims mismatch: got id=%d username=%s", claims.ID, claims.Username)
	}
}

// TestParseRefreshToken_BothValid 验证两者都有效时正常续签。
func TestParseRefreshToken_BothValid(t *testing.T) {
	accessToken, refreshToken, err := GenerateToken(10, "bob", 0)
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	newA, newR, err := ParseRefreshToken(accessToken, refreshToken)
	if err != nil {
		t.Fatalf("ParseRefreshToken: %v", err)
	}
	if newA == "" || newR == "" {
		t.Fatal("empty tokens returned")
	}
}

// TestParseRefreshToken_BothExpired 验证两者均过期时返回错误。
func TestParseRefreshToken_BothExpired(t *testing.T) {
	expiredAccess, err := makeTokenWithExpiry(1, "eve", time.Now().Add(-2*time.Second))
	if err != nil {
		t.Fatalf("makeTokenWithExpiry access: %v", err)
	}

	// 使用极短 refresh 过期时间（已过）—— 覆盖 consts，临时 patch 方式：
	// 直接构造一个已过期的 refresh token
	expiredRefreshClaims := jwt.RegisteredClaims{
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Second)),
		Issuer:    "mall",
	}
	expiredRefresh, err := jwt.NewWithClaims(jwt.SigningMethodHS256, expiredRefreshClaims).SignedString(secret())
	if err != nil {
		t.Fatalf("sign expired refresh: %v", err)
	}

	_, _, err = ParseRefreshToken(expiredAccess, expiredRefresh)
	if err == nil {
		t.Fatal("expected error when both tokens expired, got nil")
	}
}

// TestParseRefreshToken_InvalidSignatureAccess 验证 access token 签名错误时应返回错误（不允许跳过）。
func TestParseRefreshToken_InvalidSignatureAccess(t *testing.T) {
	// 用不同 secret 签发的 access token
	badClaims := Claims{
		ID:       99,
		Username: "hacker",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Second)),
			Issuer:    "mall",
		},
	}
	tampered, err := jwt.NewWithClaims(jwt.SigningMethodHS256, badClaims).SignedString([]byte("wrong-secret"))
	if err != nil {
		t.Fatalf("sign tampered: %v", err)
	}

	_, validRefresh, err := GenerateToken(1, "legit", 0)
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}

	_, _, err = ParseRefreshToken(tampered, validRefresh)
	if err == nil {
		t.Fatal("expected error for tampered access token signature, got nil")
	}
}

// TestParseRefreshToken_UsesAccessClaimsAfterExpiry 验证 access token 过期后仍然使用其中的 ID/Username 续签。
func TestParseRefreshToken_UsesAccessClaimsAfterExpiry(t *testing.T) {
	expiredAccess, err := makeTokenWithExpiry(77, "carol", time.Now().Add(-time.Second))
	if err != nil {
		t.Fatalf("makeTokenWithExpiry: %v", err)
	}
	_, validRefresh, err := GenerateToken(77, "carol", 0)
	if err != nil {
		t.Fatalf("GenerateToken refresh: %v", err)
	}

	newAccess, _, err := ParseRefreshToken(expiredAccess, validRefresh)
	if err != nil {
		t.Fatalf("ParseRefreshToken: %v", err)
	}

	claims, err := ParseToken(newAccess)
	if err != nil {
		t.Fatalf("parse new access: %v", err)
	}
	// 续签的新 token 必须来自过期 access token 里的身份，而非 refresh token 里（refresh token 没有 ID/Username）
	if claims.ID != 77 {
		t.Errorf("want ID=77 from expired access claims, got %d", claims.ID)
	}
	if claims.Username != "carol" {
		t.Errorf("want Username=carol, got %s", claims.Username)
	}
	// 新 token 的过期时间应该在将来
	if claims.ExpiresAt == nil || time.Now().After(claims.ExpiresAt.Time) {
		t.Errorf("new access token should have future expiry")
	}
}

// TestParseRefreshToken_AccessExpiry_Duration 验证续签后 access token 有效期大约等于 AccessTokenExpireDuration。
func TestParseRefreshToken_AccessExpiry_Duration(t *testing.T) {
	expiredAccess, err := makeTokenWithExpiry(5, "dave", time.Now().Add(-time.Second))
	if err != nil {
		t.Fatalf("makeTokenWithExpiry: %v", err)
	}
	_, validRefresh, err := GenerateToken(5, "dave", 0)
	if err != nil {
		t.Fatalf("GenerateToken refresh: %v", err)
	}

	before := time.Now()
	newAccess, _, err := ParseRefreshToken(expiredAccess, validRefresh)
	if err != nil {
		t.Fatalf("ParseRefreshToken: %v", err)
	}

	claims, err := ParseToken(newAccess)
	if err != nil {
		t.Fatalf("parse new access: %v", err)
	}

	expectedExpiry := before.Add(consts.AccessTokenExpireDuration)
	diff := claims.ExpiresAt.Time.Sub(expectedExpiry)
	if diff < -5*time.Second || diff > 5*time.Second {
		t.Errorf("new access token expiry %v not within 5s of expected %v", claims.ExpiresAt.Time, expectedExpiry)
	}
}

// TestTokenVersion_SignedIntoClaims 验证签发时版本号写入 claims、解析后能取回。
func TestTokenVersion_SignedIntoClaims(t *testing.T) {
	access, _, err := GenerateToken(8, "frank", 7)
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	claims, err := ParseToken(access)
	if err != nil {
		t.Fatalf("ParseToken: %v", err)
	}
	if claims.TokenVersion != 7 {
		t.Errorf("want TokenVersion=7, got %d", claims.TokenVersion)
	}
}

// TestParseRefreshToken_PreservesTokenVersion 验证续签透传旧版本号而非"洗白"：
// 被 bump 掉的旧 token 即使续签成功，新 token 仍带旧版本号，会被中间件拒绝。
func TestParseRefreshToken_PreservesTokenVersion(t *testing.T) {
	expiredClaims := Claims{
		ID:           9,
		Username:     "grace",
		TokenVersion: 5,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Second)),
			Issuer:    "mall",
		},
	}
	expiredAccess, err := jwt.NewWithClaims(jwt.SigningMethodHS256, expiredClaims).SignedString(secret())
	if err != nil {
		t.Fatalf("sign expired access: %v", err)
	}
	_, validRefresh, err := GenerateToken(9, "grace", 5)
	if err != nil {
		t.Fatalf("GenerateToken refresh: %v", err)
	}

	newAccess, _, err := ParseRefreshToken(expiredAccess, validRefresh)
	if err != nil {
		t.Fatalf("ParseRefreshToken: %v", err)
	}
	claims, err := ParseToken(newAccess)
	if err != nil {
		t.Fatalf("parse new access: %v", err)
	}
	if claims.TokenVersion != 5 {
		t.Errorf("续签必须透传版本号：want 5, got %d", claims.TokenVersion)
	}
}
