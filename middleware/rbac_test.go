package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/pkg/utils/ctl"
)

// TestRequireRole 白盒验证放行/拦截：直接播种 roleCache 绕开 DB，
// 只测中间件本身的角色判定（响应恒为 HTTP 200，通过与否看下游是否执行）。
func TestRequireRole(t *testing.T) {
	gin.SetMode(gin.TestMode)
	roleCache.Store(uint(1), roleCacheEntry{role: "merchant", expires: time.Now().Add(time.Minute)})
	roleCache.Store(uint(2), roleCacheEntry{role: "user", expires: time.Now().Add(time.Minute)})
	roleCache.Store(uint(3), roleCacheEntry{role: "admin", expires: time.Now().Add(time.Minute)})
	defer func() {
		for _, id := range []uint{1, 2, 3} {
			InvalidateRoleCache(id)
		}
	}()

	pass := func(uid uint, allowed ...string) bool {
		passed := false
		r := gin.New()
		r.GET("/t",
			func(c *gin.Context) {
				c.Request = c.Request.WithContext(ctl.NewContext(c.Request.Context(), &ctl.UserInfo{Id: uid}))
			},
			RequireRole(allowed...),
			func(c *gin.Context) { passed = true },
		)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/t", nil))
		return passed
	}

	if !pass(1, "merchant", "admin") {
		t.Fatal("merchant 应通过 merchant 墙")
	}
	if !pass(3, "merchant", "admin") {
		t.Fatal("admin 应通过 merchant 墙（admin 天然包含 merchant 权限）")
	}
	if pass(2, "merchant", "admin") {
		t.Fatal("普通 user 不应通过 merchant 墙")
	}
	if pass(1, "admin") {
		t.Fatal("merchant 不应通过 admin 墙")
	}
}
