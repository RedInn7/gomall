package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	util "github.com/RedInn7/gomall/pkg/utils/log"
)

func quietLogs(t *testing.T) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	logger := logrus.New()
	logger.Out = io.Discard
	previous := util.LogrusObj
	util.LogrusObj = logger
	t.Cleanup(func() { util.LogrusObj = previous })
}

func TestCircuitBreakerRecoversAfterPanickingHalfOpenProbe(t *testing.T) {
	quietLogs(t)
	const openTimeout = 50 * time.Millisecond
	mode, calls := "fail", 0
	r := gin.New()
	r.Use(gin.RecoveryWithWriter(io.Discard))
	r.GET("/pay", CircuitBreaker(CircuitBreakerOption{FailureThreshold: 1, OpenTimeout: openTimeout, HalfOpenMaxReq: 1}), func(c *gin.Context) {
		calls++
		switch mode {
		case "fail":
			c.Status(http.StatusInternalServerError)
		case "panic":
			panic("probe blew up")
		default:
			c.Status(http.StatusOK)
		}
	})
	hit := func() int {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/pay", nil))
		return w.Code
	}

	hit() // 一次 5xx 即熔断
	time.Sleep(openTimeout)
	mode = "panic"
	if code := hit(); code != http.StatusInternalServerError || calls != 2 {
		t.Fatalf("half-open probe should reach the handler and panic into 500: code=%d calls=%d", code, calls)
	}

	mode = "ok"
	hit()
	if calls != 2 {
		t.Fatal("panicking probe must reopen the breaker")
	}
	time.Sleep(openTimeout)
	if code := hit(); code != http.StatusOK || calls != 3 {
		t.Fatalf("breaker stuck after a panicking probe: code=%d calls=%d", code, calls)
	}
	if code := hit(); code != http.StatusOK || calls != 4 {
		t.Fatalf("successful probe must close the breaker: code=%d calls=%d", code, calls)
	}
}
