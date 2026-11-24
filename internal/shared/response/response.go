package response

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/go-sql-driver/mysql"
	"github.com/redis/go-redis/v9"

	conf "github.com/RedInn7/gomall/config"
	"github.com/RedInn7/gomall/pkg/e"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
)

// ErrorResponse 将校验错误翻译成可读文案，其余错误透传原始信息。
func ErrorResponse(ctx *gin.Context, err error) *ctl.Response {
	if ve, ok := err.(validator.ValidationErrors); ok {
		for _, fieldError := range ve {
			field := conf.T(fmt.Sprintf("Field.%s", fieldError.Field()))
			tag := conf.T(fmt.Sprintf("Tag.%s", fieldError.Tag()))
			return ctl.RespError(ctx, err, fmt.Sprintf("%s%s", field, tag))
		}
	}

	if _, ok := err.(*json.UnmarshalTypeError); ok {
		return ctl.RespError(ctx, err, "JSON类型不匹配")
	}

	// 带码错误（e.New 构造）直接透出其业务码 + 客服话术；裸 error 落 500 兜底。
	// 这一处翻译取代了过去各领域自己写的 error→code 映射。
	if code := e.CodeOf(err); code != e.ERROR {
		return ctl.RespError(ctx, err, e.GetMsg(code), code)
	}

	// 基础设施报错（数据库、Redis、网络、超时）的原文里有表名、SQL、地址，不能给前端。
	// 原始错误已由 Fail 记进日志，这里换成通用话术。
	if isInfraError(err) {
		return ctl.RespError(ctx, errInternal, errInternal.Error(), e.ERROR)
	}

	return ctl.RespError(ctx, err, err.Error(), e.ERROR)
}

// errInternal 基础设施报错时返回给前端的通用错误。
var errInternal = errors.New("系统繁忙，请稍后再试")

// isInfraError 错误链里是否有数据库、Redis、网络或超时错误。业务层自己 errors.New 的提示不算，照常透出。
func isInfraError(err error) bool {
	var (
		mysqlErr *mysql.MySQLError
		redisErr redis.Error
		netErr   net.Error
	)
	switch {
	case errors.As(err, &mysqlErr), errors.As(err, &redisErr), errors.As(err, &netErr):
		return true
	case errors.Is(err, driver.ErrBadConn), errors.Is(err, sql.ErrConnDone), errors.Is(err, sql.ErrTxDone),
		errors.Is(err, mysql.ErrInvalidConn),
		errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return true
	}
	// 没有专门类型的驱动报错（连接池超时、client 已关闭），以及被 %v 包装后丢了类型的，按前缀兜一层。
	// io.EOF 不算：空请求体、截断的 JSON 绑定失败也是它，那是客户端参数问题。
	msg := err.Error()
	for _, mark := range []string{"redis: ", "sql: ", "dial tcp", "invalid connection"} {
		if strings.Contains(msg, mark) {
			return true
		}
	}
	return false
}
