package middleware

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/internal/shared/response"
	"github.com/RedInn7/gomall/pkg/e"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
	"github.com/RedInn7/gomall/repository/cache"
)

// memIdempotency 内存版幂等状态机，语义对齐 repository/cache 里的 Lua 脚本
type memIdempotency struct {
	state, result map[string]string
	commitErr     error
}

func installMemIdempotency(t *testing.T) *memIdempotency {
	t.Helper()
	m := &memIdempotency{state: map[string]string{}, result: map[string]string{}}
	prevAcquire, prevCommit, prevRelease := acquireIdempotency, commitIdempotency, releaseIdempotency
	acquireIdempotency = func(_ context.Context, key string) (int64, string, error) {
		switch m.state[key] {
		case cache.IdempotencyStateInit:
			m.state[key] = cache.IdempotencyStateProcessing
			return 1, "", nil
		case cache.IdempotencyStateDone:
			return 2, m.result[key], nil
		case cache.IdempotencyStateProcessing:
			return 3, "", nil
		}
		return 0, "", nil
	}
	commitIdempotency = func(_ context.Context, key, result string) error {
		if m.commitErr != nil {
			return m.commitErr
		}
		m.state[key], m.result[key] = cache.IdempotencyStateDone, result
		return nil
	}
	releaseIdempotency = func(_ context.Context, key string) error {
		m.state[key] = cache.IdempotencyStateInit
		return nil
	}
	t.Cleanup(func() {
		acquireIdempotency, commitIdempotency, releaseIdempotency = prevAcquire, prevCommit, prevRelease
	})
	return m
}

const testUserID = 7

// idempotentRoute 挂一个带 Idempotency 的路由，handler 由调用方决定每次的行为
func idempotentRoute(handler gin.HandlerFunc) func(token string) *httptest.ResponseRecorder {
	r := gin.New()
	r.Use(gin.RecoveryWithWriter(io.Discard), func(c *gin.Context) {
		c.Request = c.Request.WithContext(ctl.NewContext(c.Request.Context(), &ctl.UserInfo{Id: testUserID}))
	})
	r.POST("/paydown", Idempotency(), handler)
	return func(token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/paydown", nil)
		req.Header.Set(IdempotencyHeader, token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
}

func TestIdempotencyLetsSameKeyRetryAfterBusinessFailure(t *testing.T) {
	quietLogs(t)
	m := installMemIdempotency(t)
	key := cache.IdempotencyTokenKey(testUserID, "tok-1")
	m.state[key] = cache.IdempotencyStateInit

	calls := 0
	send := idempotentRoute(func(c *gin.Context) {
		if calls++; calls == 1 {
			response.Fail(c, e.New(e.ErrorNotCompare)) // 例如支付密码错误：HTTP 仍是 200
			return
		}
		response.OK(c, "paid")
	})

	send("tok-1")
	if m.state[key] != cache.IdempotencyStateInit {
		t.Fatalf("business failure must release the key, state=%q", m.state[key])
	}
	if w := send("tok-1"); w.Header().Get("X-Idempotent-Replay") != "" || calls != 2 {
		t.Fatalf("retry with the same key must run the handler again, calls=%d replay=%q", calls, w.Header().Get("X-Idempotent-Replay"))
	}
	if m.state[key] != cache.IdempotencyStateDone {
		t.Fatalf("successful retry must be committed, state=%q", m.state[key])
	}
}

func TestIdempotencyReleasesKeyWhenHandlerPanics(t *testing.T) {
	quietLogs(t)
	m := installMemIdempotency(t)
	key := cache.IdempotencyTokenKey(testUserID, "tok-2")
	m.state[key] = cache.IdempotencyStateInit

	send := idempotentRoute(func(*gin.Context) { panic("handler bug") })
	if w := send("tok-2"); w.Code != http.StatusInternalServerError {
		t.Fatalf("panic must still reach Recovery, code=%d", w.Code)
	}
	if m.state[key] != cache.IdempotencyStateInit {
		t.Fatalf("panic must release the key instead of leaving it processing, state=%q", m.state[key])
	}
}

func TestIdempotencyKeepsProcessingWhenCommitKeepsFailing(t *testing.T) {
	quietLogs(t)
	m := installMemIdempotency(t)
	m.commitErr = errors.New("redis down")
	key := cache.IdempotencyTokenKey(testUserID, "tok-3")
	m.state[key] = cache.IdempotencyStateInit

	send := idempotentRoute(func(c *gin.Context) { response.OK(c, "paid") })
	send("tok-3")
	if m.state[key] != cache.IdempotencyStateProcessing {
		t.Fatalf("succeeded request must not be reopened for another run, state=%q", m.state[key])
	}
}
