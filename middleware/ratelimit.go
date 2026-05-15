package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"

	"github.com/RedInn7/gomall/pkg/e"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
	"github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/cache"
)

// TokenBucket 单实例 IP 维度令牌桶限流
//   rps:   每秒放令牌速率
//   burst: 桶容量
func TokenBucket(rps rate.Limit, burst int) gin.HandlerFunc {
	var (
		mu       sync.Mutex
		limiters = make(map[string]*rate.Limiter)
	)
	get := func(ip string) *rate.Limiter {
		mu.Lock()
		defer mu.Unlock()
		l, ok := limiters[ip]
		if !ok {
			l = rate.NewLimiter(rps, burst)
			limiters[ip] = l
		}
		return l
	}
	return func(c *gin.Context) {
		if !get(c.ClientIP()).Allow() {
			c.JSON(http.StatusOK, gin.H{
				"status": e.ErrRateLimitExceeded,
				"msg":    e.GetMsg(e.ErrRateLimitExceeded),
				"data":   "请求过于频繁",
			})
			c.Abort()
			return
		}
		c.Next()
	}
}

type SlidingWindowOption struct {
	Scope    string        // 限流作用域，作为 redis key 的一部分（如 "seckill"）
	Window   time.Duration // 窗口大小
	Limit    int64         // 窗口内最大请求数
	ByUser   bool          // true: 按用户 id 限流；false: 按 IP
}

// SlidingWindow Redis 滑动窗口分布式限流
func SlidingWindow(opt SlidingWindowOption) gin.HandlerFunc {
	if opt.Scope == "" {
		opt.Scope = "default"
	}
	if opt.Window <= 0 {
		opt.Window = time.Second
	}
	if opt.Limit <= 0 {
		opt.Limit = 100
	}
	return func(c *gin.Context) {
		var identifier string
		if opt.ByUser {
			u, err := ctl.GetUserInfo(c.Request.Context())
			if err != nil {
				identifier = c.ClientIP()
			} else {
				identifier = formatUint(u.Id)
			}
		} else {
			identifier = c.ClientIP()
		}

		key := cache.RateLimitKey(opt.Scope, identifier)
		nowMS := time.Now().UnixMilli()
		buf := make([]byte, 6)
		_, _ = rand.Read(buf)
		member := formatInt(nowMS) + "-" + hex.EncodeToString(buf)

		allowed, count, err := cache.SlidingWindowAllow(c.Request.Context(), key,
			opt.Window.Milliseconds(), opt.Limit, nowMS, member)
		if err != nil {
			log.LogrusObj.Errorln("sliding window error:", err)
			c.Next()
			return
		}
		if !allowed {
			c.JSON(http.StatusOK, gin.H{
				"status": e.ErrRateLimitExceeded,
				"msg":    e.GetMsg(e.ErrRateLimitExceeded),
				"data":   gin.H{"current": count, "limit": opt.Limit},
			})
			c.Abort()
			return
		}
		c.Next()
	}
}

func formatUint(v uint) string {
	return formatInt(int64(v))
}

func formatInt(v int64) string {
	const digits = "0123456789"
	if v == 0 {
		return "0"
	}
	negative := v < 0
	if negative {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = digits[v%10]
		v /= 10
	}
	if negative {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
