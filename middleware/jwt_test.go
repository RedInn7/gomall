package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	conf "github.com/RedInn7/gomall/config"
	util "github.com/RedInn7/gomall/pkg/utils/jwt"
	utilLog "github.com/RedInn7/gomall/pkg/utils/log"
)

func init() {
	// 初始化测试用 config 与 logger，避免依赖外部文件 / nil 指针
	conf.Config = &conf.Conf{
		EncryptSecret: &conf.EncryptSecret{
			JwtSecret: "test-secret-key-for-unit-tests",
		},
	}
	utilLog.InitLog()
}

// TestAuthMiddleware_TokenVersionGate 验证版本号撤销门全链路：
// 版本号匹配放行 → 模拟改密码（bump + 清缓存）后同一 token 立即被拒 → 重新登录的新 token 放行。
func TestAuthMiddleware_TokenVersionGate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dbVer := uint(0) // 模拟 users.token_version
	SetTokenVersionLookup(func(ctx context.Context, userId uint) (uint, error) {
		return dbVer, nil
	})
	defer func() {
		SetTokenVersionLookup(nil)
		InvalidateTokenVersionCache(1)
	}()

	request := func(access, refresh string) (passed bool) {
		r := gin.New()
		r.GET("/t", AuthMiddleware(), func(c *gin.Context) { passed = true })
		req := httptest.NewRequest(http.MethodGet, "/t", nil)
		req.Header.Set("access_token", access)
		req.Header.Set("refresh_token", refresh)
		r.ServeHTTP(httptest.NewRecorder(), req)
		return passed
	}

	access, refresh, err := util.GenerateToken(1, "alice", 0)
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	if !request(access, refresh) {
		t.Fatal("版本号匹配的 token 应放行")
	}

	// 模拟改密码：版本号 bump 并清缓存 → 同一个 token 下一个请求即被拒
	dbVer = 1
	InvalidateTokenVersionCache(1)
	if request(access, refresh) {
		t.Fatal("bump 版本号后旧 token 应被拒")
	}

	// 重新登录拿到新版本号 token → 放行
	access2, refresh2, err := util.GenerateToken(1, "alice", 1)
	if err != nil {
		t.Fatalf("GenerateToken v1: %v", err)
	}
	if !request(access2, refresh2) {
		t.Fatal("重新登录的新版本号 token 应放行")
	}
}

// TestAuthMiddleware_TokenVersionFailClosed 验证 lookup 未注入时拒绝（fail-closed）而非放行。
func TestAuthMiddleware_TokenVersionFailClosed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	SetTokenVersionLookup(nil)
	InvalidateTokenVersionCache(2)

	access, refresh, err := util.GenerateToken(2, "bob", 0)
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	passed := false
	r := gin.New()
	r.GET("/t", AuthMiddleware(), func(c *gin.Context) { passed = true })
	req := httptest.NewRequest(http.MethodGet, "/t", nil)
	req.Header.Set("access_token", access)
	req.Header.Set("refresh_token", refresh)
	r.ServeHTTP(httptest.NewRecorder(), req)
	if passed {
		t.Fatal("撤销机制未配置时应 fail-closed 拒绝请求")
	}
}
