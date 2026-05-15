package middleware

import (
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/pkg/e"
)

type circuitState int32

const (
	stateClosed   circuitState = 0
	stateOpen     circuitState = 1
	stateHalfOpen circuitState = 2
)

// CircuitBreakerOption 三态熔断器配置
type CircuitBreakerOption struct {
	FailureThreshold int64         // 关闭态触发熔断的连续失败数
	OpenTimeout      time.Duration // open → half-open 等待时间
	HalfOpenMaxReq   int64         // 半开态允许的探测请求数
}

type circuitBreaker struct {
	opt         CircuitBreakerOption
	state       atomic.Int32 // circuitState
	failures    atomic.Int64
	openedAt    atomic.Int64 // unix nano
	halfOpenReq atomic.Int64
	mu          sync.Mutex
}

// CircuitBreaker 简易三态熔断中间件。
//   - 5xx 或 panic 视为失败
//   - 失败连续达到 FailureThreshold → 进入 Open
//   - Open 超过 OpenTimeout → 放 HalfOpenMaxReq 个请求探测
//   - 半开期任一失败 → 重新 Open；全部成功 → 回到 Closed
func CircuitBreaker(opt CircuitBreakerOption) gin.HandlerFunc {
	if opt.FailureThreshold <= 0 {
		opt.FailureThreshold = 5
	}
	if opt.OpenTimeout <= 0 {
		opt.OpenTimeout = 10 * time.Second
	}
	if opt.HalfOpenMaxReq <= 0 {
		opt.HalfOpenMaxReq = 3
	}
	cb := &circuitBreaker{opt: opt}

	return func(c *gin.Context) {
		if err := cb.allow(); err != nil {
			c.JSON(http.StatusOK, gin.H{
				"status": e.ErrCircuitOpen,
				"msg":    e.GetMsg(e.ErrCircuitOpen),
				"data":   err.Error(),
			})
			c.Abort()
			return
		}

		c.Next()

		failed := len(c.Errors) > 0 || c.Writer.Status() >= http.StatusInternalServerError
		cb.report(failed)
	}
}

var errCircuitOpen = errors.New("circuit open")

func (cb *circuitBreaker) allow() error {
	switch circuitState(cb.state.Load()) {
	case stateClosed:
		return nil
	case stateOpen:
		if time.Now().UnixNano()-cb.openedAt.Load() < int64(cb.opt.OpenTimeout) {
			return errCircuitOpen
		}
		cb.mu.Lock()
		defer cb.mu.Unlock()
		// 在锁内 double-check
		if circuitState(cb.state.Load()) == stateOpen &&
			time.Now().UnixNano()-cb.openedAt.Load() >= int64(cb.opt.OpenTimeout) {
			cb.state.Store(int32(stateHalfOpen))
			cb.halfOpenReq.Store(0)
		}
		if circuitState(cb.state.Load()) == stateHalfOpen {
			if cb.halfOpenReq.Add(1) > cb.opt.HalfOpenMaxReq {
				return errCircuitOpen
			}
			return nil
		}
		return errCircuitOpen
	case stateHalfOpen:
		if cb.halfOpenReq.Add(1) > cb.opt.HalfOpenMaxReq {
			return errCircuitOpen
		}
		return nil
	}
	return nil
}

func (cb *circuitBreaker) report(failed bool) {
	switch circuitState(cb.state.Load()) {
	case stateClosed:
		if failed {
			if cb.failures.Add(1) >= cb.opt.FailureThreshold {
				cb.tripOpen()
			}
		} else {
			cb.failures.Store(0)
		}
	case stateHalfOpen:
		if failed {
			cb.tripOpen()
		} else if cb.halfOpenReq.Load() >= cb.opt.HalfOpenMaxReq {
			cb.mu.Lock()
			cb.state.Store(int32(stateClosed))
			cb.failures.Store(0)
			cb.mu.Unlock()
		}
	}
}

func (cb *circuitBreaker) tripOpen() {
	cb.mu.Lock()
	cb.state.Store(int32(stateOpen))
	cb.openedAt.Store(time.Now().UnixNano())
	cb.failures.Store(0)
	cb.mu.Unlock()
}
