package middleware

import (
	"bytes"
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/internal/shared/response"
	"github.com/RedInn7/gomall/pkg/e"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
	"github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/cache"
)

const IdempotencyHeader = "Idempotency-Key"

const (
	// 提交/释放属于请求返回后的清理动作，使用独立的后台超时，避免请求 ctx 已取消导致清理失败
	idempotencyCleanupTimeout = 3 * time.Second
	idempotencyCommitRetries  = 3
)

// 幂等状态机的 Redis 操作，单测替换成内存实现
var (
	acquireIdempotency = cache.AcquireIdempotencyLock
	commitIdempotency  = cache.CommitIdempotencyResult
	releaseIdempotency = cache.ReleaseIdempotencyLock
)

// responseRecorder 拷贝写入的响应体，便于幂等命中时回放
type responseRecorder struct {
	gin.ResponseWriter
	body *bytes.Buffer
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	r.body.Write(b)
	return r.ResponseWriter.Write(b)
}

func (r *responseRecorder) WriteString(s string) (int, error) {
	r.body.WriteString(s)
	return r.ResponseWriter.WriteString(s)
}

// Idempotency 幂等中间件：Header 中带 Idempotency-Key
// 状态机由 cache.AcquireIdempotencyLock 中的 Lua 脚本驱动
func Idempotency() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := c.GetHeader(IdempotencyHeader)
		if token == "" {
			c.JSON(http.StatusOK, gin.H{
				"status": e.InvalidParams,
				"msg":    e.GetMsg(e.InvalidParams),
				"data":   "缺少 Idempotency-Key",
			})
			c.Abort()
			return
		}

		user, err := ctl.GetUserInfo(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"status": e.ErrorAuthCheckTokenFail,
				"msg":    e.GetMsg(e.ErrorAuthCheckTokenFail),
				"data":   "未获取到用户身份",
			})
			c.Abort()
			return
		}

		key := cache.IdempotencyTokenKey(user.Id, token)
		state, cached, err := acquireIdempotency(c.Request.Context(), key)
		if err != nil {
			log.LogrusObj.Errorln("idempotency lua error:", err)
			c.JSON(http.StatusOK, gin.H{
				"status": e.ERROR,
				"msg":    e.GetMsg(e.ERROR),
				"data":   "幂等服务异常",
			})
			c.Abort()
			return
		}

		switch state {
		case 0:
			c.JSON(http.StatusOK, gin.H{
				"status": e.ErrIdempotencyTokenInvalid,
				"msg":    e.GetMsg(e.ErrIdempotencyTokenInvalid),
				"data":   "token 不存在或已过期",
			})
			c.Abort()
			return
		case 2:
			c.Header("X-Idempotent-Replay", "true")
			c.Data(http.StatusOK, "application/json; charset=utf-8", []byte(cached))
			c.Abort()
			return
		case 3:
			c.JSON(http.StatusOK, gin.H{
				"status": e.ErrIdempotencyInProgress,
				"msg":    e.GetMsg(e.ErrIdempotencyInProgress),
				"data":   "请求正在处理中",
			})
			c.Abort()
			return
		}

		recorder := &responseRecorder{ResponseWriter: c.Writer, body: bytes.NewBuffer(nil)}
		c.Writer = recorder

		// handler panic 时 c.Next 不会正常返回，key 会停在 processing 直到 TTL 过期；
		// 先回退到 init 让客户端能重试，再把 panic 原样抛给外层 Recovery。
		defer func() {
			if r := recover(); r != nil {
				releaseIdempotencyLock(key)
				panic(r)
			}
		}()
		c.Next()

		// response.Fail 的业务失败（余额不足、密码错误等）也是 HTTP 200，靠它在 ctx 上的标记识别；
		// 失败不缓存成 done，同一个 key 改正后还能重试
		if len(c.Errors) > 0 || recorder.Status() >= http.StatusBadRequest || response.Failed(c) {
			releaseIdempotencyLock(key)
			return
		}

		commitIdempotencyResult(key, recorder.body.String())
	}
}

// commitIdempotencyResult 将幂等记录从 processing 推进到 done 并缓存响应体。
// 业务副作用此时已落库、2xx 也已返回给客户端，因此提交失败不能静默吞掉：
// 在后台 context 内带超时重试若干次；仍失败就让记录停在 processing 等 TTL 过期。
// 不能回退到 init：那会让同一 token 再放行一次，把已经成功的副作用重复执行。
func commitIdempotencyResult(key, result string) {
	for i := 0; i < idempotencyCommitRetries; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), idempotencyCleanupTimeout)
		err := commitIdempotency(ctx, key, result)
		cancel()
		if err == nil {
			return
		}
		log.LogrusObj.Errorln("idempotency commit error:", err)
	}
	log.LogrusObj.Errorln("idempotency commit gave up, token stays processing until TTL expires")
}

// releaseIdempotencyLock 在后台 context 内把幂等记录回退到 init，
// 使用独立超时避免请求 ctx 已取消导致清理本身失败。
func releaseIdempotencyLock(key string) {
	ctx, cancel := context.WithTimeout(context.Background(), idempotencyCleanupTimeout)
	defer cancel()
	if err := releaseIdempotency(ctx, key); err != nil {
		log.LogrusObj.Errorln("idempotency release error:", err)
	}
}
