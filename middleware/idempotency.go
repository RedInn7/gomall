package middleware

import (
	"bytes"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/pkg/e"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
	"github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/cache"
)

const IdempotencyHeader = "Idempotency-Key"

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
		state, cached, err := cache.AcquireIdempotencyLock(c.Request.Context(), key)
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

		c.Next()

		if len(c.Errors) > 0 || recorder.Status() >= http.StatusBadRequest {
			if err := cache.ReleaseIdempotencyLock(c.Request.Context(), key); err != nil {
				log.LogrusObj.Errorln("idempotency release error:", err)
			}
			return
		}

		if err := cache.CommitIdempotencyResult(c.Request.Context(), key, recorder.body.String()); err != nil {
			log.LogrusObj.Errorln("idempotency commit error:", err)
		}
	}
}
