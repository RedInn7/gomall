package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// flipHandler 由 atomic.Bool 控制行为：true 返回 500，false 返回 200
type flipHandler struct{ shouldFail atomic.Bool }

func (f *flipHandler) gin() gin.HandlerFunc {
	return func(c *gin.Context) {
		if f.shouldFail.Load() {
			c.Status(http.StatusInternalServerError)
			return
		}
		c.Status(http.StatusOK)
	}
}

func newCBRouter(opt CircuitBreakerOption, h gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/test", CircuitBreaker(opt), h)
	return r
}

func call(t *testing.T, r *gin.Engine) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/test", nil))
	return w
}

func TestCircuitBreaker_OpensAfterFailures(t *testing.T) {
	flip := &flipHandler{}
	flip.shouldFail.Store(true)
	r := newCBRouter(
		CircuitBreakerOption{FailureThreshold: 3, OpenTimeout: time.Second, HalfOpenMaxReq: 1},
		flip.gin(),
	)
	for i := 0; i < 3; i++ {
		if w := call(t, r); w.Code != http.StatusInternalServerError {
			t.Fatalf("call %d: want 500 in closed state, got %d", i+1, w.Code)
		}
	}
	w := call(t, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "70002") {
		t.Fatalf("expect circuit-open response with status 70002, got code=%d body=%s", w.Code, w.Body.String())
	}
}

func TestCircuitBreaker_HalfOpenToClosed(t *testing.T) {
	flip := &flipHandler{}
	flip.shouldFail.Store(true)
	r := newCBRouter(
		CircuitBreakerOption{FailureThreshold: 2, OpenTimeout: 80 * time.Millisecond, HalfOpenMaxReq: 2},
		flip.gin(),
	)
	// 触发 Open
	for i := 0; i < 2; i++ {
		call(t, r)
	}
	w := call(t, r)
	if !strings.Contains(w.Body.String(), "70002") {
		t.Fatalf("expect open, got body=%s", w.Body.String())
	}

	// 等到超时窗口
	time.Sleep(120 * time.Millisecond)

	// 切换为成功，半开期允许 2 个探测请求通过且都成功 → 回到 Closed
	flip.shouldFail.Store(false)
	for i := 0; i < 2; i++ {
		if w := call(t, r); w.Code != http.StatusOK || strings.Contains(w.Body.String(), "70002") {
			t.Fatalf("half-open probe %d: want pass-through 200, got code=%d body=%s", i+1, w.Code, w.Body.String())
		}
	}

	// 此时应回到 Closed，后续请求继续正常
	if w := call(t, r); w.Code != http.StatusOK {
		t.Fatalf("after recovery want 200, got %d", w.Code)
	}
}

func TestCircuitBreaker_HalfOpenFailReturnsToOpen(t *testing.T) {
	flip := &flipHandler{}
	flip.shouldFail.Store(true)
	r := newCBRouter(
		CircuitBreakerOption{FailureThreshold: 2, OpenTimeout: 60 * time.Millisecond, HalfOpenMaxReq: 2},
		flip.gin(),
	)
	for i := 0; i < 2; i++ {
		call(t, r)
	}
	time.Sleep(80 * time.Millisecond)
	// 半开探测失败 → 重新 Open
	w := call(t, r)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("half-open probe should pass through but fail: want 500 got %d", w.Code)
	}
	// 紧接着应当再次熔断
	w = call(t, r)
	if !strings.Contains(w.Body.String(), "70002") {
		t.Fatalf("expect re-open, got %s", w.Body.String())
	}
}
