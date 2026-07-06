package middleware

import (
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/internal/shared/response"
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
	opt             CircuitBreakerOption
	state           atomic.Int32 // circuitState
	failures        atomic.Int64
	openedAt        atomic.Int64 // unix nano
	halfOpenReq     atomic.Int64 // 半开态已放行的探测数
	halfOpenSuccess atomic.Int64 // 半开态已成功返回的探测数
	// generation 每进入一轮半开探测自增一次，作为代际令牌。
	// 探测请求在 allow 时携带当时的 generation，report 回报时校验代际一致才生效，
	// 避免上一轮 open 期的迟到回报误触发刚恢复 Closed 的熔断器。
	generation atomic.Int64
	mu         sync.Mutex
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
		gen, err := cb.allow()
		if err != nil {
			// 熔断拒绝走统一错误出口：带码 error 自动透出 ErrCircuitOpen + 客服话术。
			response.Fail(c, e.New(e.ErrCircuitOpen))
			c.Abort()
			return
		}

		c.Next()

		failed := len(c.Errors) > 0 || c.Writer.Status() >= http.StatusInternalServerError
		cb.report(failed, gen)
	}
}

var errCircuitOpen = errors.New("circuit open")

// allow 判定请求是否放行，并返回放行时所属的代际 generation。
// 返回的 generation 仅对半开探测有意义：report 会用它校验回报是否属于当前探测轮次。
func (cb *circuitBreaker) allow() (int64, error) {
	// 快路径：Closed 直接放行；Open 且未到超时窗口直接拒绝。两者均无需加锁。
	switch circuitState(cb.state.Load()) {
	case stateClosed:
		return cb.generation.Load(), nil
	case stateOpen:
		if time.Now().UnixNano()-cb.openedAt.Load() < int64(cb.opt.OpenTimeout) {
			return 0, errCircuitOpen
		}
	}

	// 慢路径：可能要把 Open 升级为半开并放行探测，状态读写全部收进锁内保证一致。
	cb.mu.Lock()
	defer cb.mu.Unlock()

	st := circuitState(cb.state.Load())
	if st == stateOpen && time.Now().UnixNano()-cb.openedAt.Load() >= int64(cb.opt.OpenTimeout) {
		// 进入新一轮半开探测：代际自增，旧代际的迟到回报随之失效。
		cb.generation.Add(1)
		cb.state.Store(int32(stateHalfOpen))
		cb.halfOpenReq.Store(0)
		cb.halfOpenSuccess.Store(0)
		st = stateHalfOpen
	}

	if st == stateHalfOpen {
		if cb.halfOpenReq.Add(1) > cb.opt.HalfOpenMaxReq {
			return 0, errCircuitOpen
		}
		return cb.generation.Load(), nil
	}

	if st == stateClosed {
		return cb.generation.Load(), nil
	}
	return 0, errCircuitOpen
}

// report 在请求结束后回报成败。gen 为放行时携带的代际：
// 半开态的成败判定只接受与当前代际一致的回报，过期代际的迟到回报被忽略，
// 避免上一轮探测的失败误触发刚恢复 Closed 的熔断器。
func (cb *circuitBreaker) report(failed bool, gen int64) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	// 代际不一致说明是上一轮探测的迟到回报：此时熔断器可能已被其它探测推回 Closed
	// 或进入新一轮 half-open。无论当前处于哪个状态，这种过期回报都不应再影响计数，
	// 否则迟到的 failed 会误增刚恢复 Closed 的失败数甚至误触发熔断。
	if gen != cb.generation.Load() {
		return
	}

	switch circuitState(cb.state.Load()) {
	case stateClosed:
		if failed {
			if cb.failures.Add(1) >= cb.opt.FailureThreshold {
				cb.tripOpenLocked()
			}
		} else {
			cb.failures.Store(0)
		}
	case stateHalfOpen:
		if failed {
			cb.tripOpenLocked()
		} else if cb.halfOpenSuccess.Add(1) >= cb.opt.HalfOpenMaxReq {
			cb.state.Store(int32(stateClosed))
			cb.failures.Store(0)
		}
	}
}

// tripOpenLocked 切换到 Open，调用方必须已持有 cb.mu。
func (cb *circuitBreaker) tripOpenLocked() {
	cb.state.Store(int32(stateOpen))
	cb.openedAt.Store(time.Now().UnixNano())
	cb.failures.Store(0)
	cb.halfOpenReq.Store(0)
	cb.halfOpenSuccess.Store(0)
}
