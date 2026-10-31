package response

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"

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
