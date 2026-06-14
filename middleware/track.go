package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/opentracing/opentracing-go"

	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/pkg/utils/track"
)

func Jaeger() gin.HandlerFunc {
	return func(c *gin.Context) {
		traceId := c.GetHeader("uber-trace-id")
		var span opentracing.Span
		if traceId != "" {
			var err error
			span, err = track.GetParentSpan(c.FullPath(), traceId, c.Request.Header)
			if err != nil {
				// uber-trace-id 由客户端控制，解析失败时降级为本地 span，避免吞掉整条请求链
				log.LogrusObj.Warnln("parse uber-trace-id failed, fallback to local span:", err)
				span = track.StartSpan(opentracing.GlobalTracer(), c.FullPath())
			}
		} else {
			span = track.StartSpan(opentracing.GlobalTracer(), c.FullPath())
		}
		defer span.Finish()

		c.Set(consts.SpanCTX, opentracing.ContextWithSpan(c, span))
		c.Next()
	}
}
