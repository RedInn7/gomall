package middleware

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

// cacheBuffer 暂存下游 handler 写入的响应体与状态码
// 让中间件在 c.Next() 之后再决定真正回写 200 还是 304
type cacheBuffer struct {
	gin.ResponseWriter
	body   *bytes.Buffer
	status int
}

func (w *cacheBuffer) WriteHeader(code int) {
	w.status = code
}

func (w *cacheBuffer) Write(b []byte) (int, error) {
	return w.body.Write(b)
}

func (w *cacheBuffer) WriteString(s string) (int, error) {
	return w.body.WriteString(s)
}

func (w *cacheBuffer) Status() int {
	return w.status
}

func (w *cacheBuffer) Size() int {
	return w.body.Len()
}

// HTTPCache 为只读公开接口注入 ETag + Cache-Control
//
//	maxAge: Cache-Control max-age 字段，0 表示禁用强缓存只保留协商缓存
//
// 仅对 GET / HEAD 且 status=200 的响应生效；其余请求直接透传
func HTTPCache(maxAge time.Duration) gin.HandlerFunc {
	if maxAge < 0 {
		maxAge = 0
	}
	cacheControl := "public, max-age=" + strconv.FormatInt(int64(maxAge/time.Second), 10)

	return func(c *gin.Context) {
		method := c.Request.Method
		if method != http.MethodGet && method != http.MethodHead {
			c.Next()
			return
		}

		original := c.Writer
		buf := &cacheBuffer{ResponseWriter: original, body: bytes.NewBuffer(nil), status: http.StatusOK}
		c.Writer = buf

		c.Next()

		// 还原 writer，避免后续中间件再次包装
		c.Writer = original

		// 非 200 响应原样回写，不参与缓存协商
		if buf.status != http.StatusOK {
			original.WriteHeader(buf.status)
			if buf.body.Len() > 0 {
				_, _ = original.Write(buf.body.Bytes())
			}
			return
		}

		etag := weakETag(buf.body.Bytes())

		// 命中协商缓存：返回 304，不写 body
		if match := c.Request.Header.Get("If-None-Match"); match != "" && match == etag {
			h := original.Header()
			h.Set("ETag", etag)
			h.Set("Cache-Control", cacheControl)
			h.Del("Content-Length")
			h.Del("Content-Type")
			original.WriteHeader(http.StatusNotModified)
			return
		}

		h := original.Header()
		h.Set("ETag", etag)
		h.Set("Cache-Control", cacheControl)
		h.Set("Content-Length", strconv.Itoa(buf.body.Len()))
		original.WriteHeader(http.StatusOK)
		if method == http.MethodGet && buf.body.Len() > 0 {
			_, _ = original.Write(buf.body.Bytes())
		}
	}
}

// weakETag 取响应体 SHA-256 前 16 字节作为弱校验值
func weakETag(body []byte) string {
	sum := sha256.Sum256(body)
	return `W/"` + hex.EncodeToString(sum[:16]) + `"`
}
