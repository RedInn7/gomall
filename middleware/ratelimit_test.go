package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

func TestTokenBucket_BurstThenLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	// 每秒 0 个令牌、burst 3：前 3 个请求应通过，第 4 个被限
	r.GET("/x", TokenBucket(rate.Limit(0), 3), func(c *gin.Context) { c.Status(http.StatusOK) })

	for i := 0; i < 3; i++ {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("req %d want 200 got %d body=%s", i+1, w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
	if body := w.Body.String(); !contains(body, "70001") {
		t.Fatalf("4th req should be rate-limited (70001), got %s", body)
	}
}

func TestTokenBucket_PerIPIsolation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/x", TokenBucket(rate.Limit(0), 1), func(c *gin.Context) { c.Status(http.StatusOK) })

	hit := func(ip string) int {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.RemoteAddr = ip + ":12345"
		r.ServeHTTP(w, req)
		return w.Code
	}
	// IP A 用掉令牌
	if c := hit("1.1.1.1"); c != http.StatusOK {
		t.Fatalf("IP A first req want 200 got %d", c)
	}
	// IP B 还有自己的桶
	if c := hit("2.2.2.2"); c != http.StatusOK {
		t.Fatalf("IP B should have its own bucket, want 200 got %d", c)
	}
	// IP A 第二次被拒
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.RemoteAddr = "1.1.1.1:12345"
	r.ServeHTTP(w, req)
	if !contains(w.Body.String(), "70001") {
		t.Fatalf("IP A 2nd req should be limited, body=%s", w.Body.String())
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
