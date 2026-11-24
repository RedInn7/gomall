package response

import (
	"errors"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/go-sql-driver/mysql"
	"github.com/redis/go-redis/v9"

	"github.com/RedInn7/gomall/pkg/e"

	conf "github.com/RedInn7/gomall/config"
)

func TestErrorResponseTranslatesValidationError(t *testing.T) {
	type req struct {
		UserName string `validate:"required"`
	}
	verr := validator.New().Struct(req{})
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	// 字典没加载时不能 panic，退回字段名和校验 tag
	conf.Dictinary = nil
	if got := ErrorResponse(ctx, verr).Data; got != "UserNamerequired" {
		t.Fatalf("without locales: got %q", got)
	}

	if err := conf.LoadLocales("../../../config/locales/zh-cn.yaml"); err != nil {
		t.Fatal(err)
	}
	if got := ErrorResponse(ctx, verr).Data; got != "用户名必须存在，而且不能为空" {
		t.Fatalf("with zh-cn locales: got %q", got)
	}
}

// 数据库 / Redis 报错的原文不能回给前端；业务层自己写的提示和带码错误照常透出。
func TestErrorResponseHidesInfraErrors(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	infra := []error{
		&mysql.MySQLError{Number: 1146, Message: "Table 'mall.red_packet' doesn't exist"},
		fmt.Errorf("claim tx: %w", &mysql.MySQLError{Number: 1213, Message: "Deadlock found on red_packet"}),
		redis.Nil,
		mysql.ErrInvalidConn,
		errors.New("redis: connection pool timeout"),
		fmt.Errorf("claim tx: %v", &mysql.MySQLError{Number: 1213, Message: "sql: transaction has already been committed"}),
	}
	for _, err := range infra {
		r := ErrorResponse(ctx, err)
		if r.Status != e.ERROR {
			t.Fatalf("%v: status %d", err, r.Status)
		}
		for _, leaked := range []string{"red_packet", "Table", "Deadlock", "redis", "connection"} {
			if strings.Contains(fmt.Sprint(r.Data), leaked) || strings.Contains(r.Error, leaked) {
				t.Fatalf("%v leaked %q: data=%v error=%q", err, leaked, r.Data, r.Error)
			}
		}
	}

	if r := ErrorResponse(ctx, errors.New("总额必须 ≥ 份数")); r.Data != "总额必须 ≥ 份数" {
		t.Fatalf("business hint should pass through, got %v", r.Data)
	}
	// 空请求体绑定失败是客户端参数问题，不能说成系统繁忙
	if r := ErrorResponse(ctx, io.EOF); r.Data == errInternal.Error() {
		t.Fatalf("EOF from an empty body must not be reported as infra error")
	}
	if r := ErrorResponse(ctx, e.New(e.ErrRedPacketDrained)); r.Status != e.ErrRedPacketDrained {
		t.Fatalf("coded error should keep its code, got %d", r.Status)
	}
}
