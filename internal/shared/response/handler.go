package response

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/pkg/utils/ctl"
	"github.com/RedInn7/gomall/pkg/utils/log"
)

// Bind 绑定并校验请求体（等价 ctx.ShouldBind）。校验失败时已记日志并写好错误响应，
// 返回 ok=false，handler 直接 return 即可，无需重复错误处理。
func Bind[T any](ctx *gin.Context) (*T, bool) {
	var req T
	if err := ctx.ShouldBind(&req); err != nil {
		Fail(ctx, err)
		return nil, false
	}
	return &req, true
}

// BindQuery 仅从 query string 绑定（等价 ctx.ShouldBindQuery），其余同 Bind。
func BindQuery[T any](ctx *gin.Context) (*T, bool) {
	var req T
	if err := ctx.ShouldBindQuery(&req); err != nil {
		Fail(ctx, err)
		return nil, false
	}
	return &req, true
}

// BindJSON 强制按 JSON 绑定（等价 ctx.ShouldBindJSON），其余同 Bind。
func BindJSON[T any](ctx *gin.Context) (*T, bool) {
	var req T
	if err := ctx.ShouldBindJSON(&req); err != nil {
		Fail(ctx, err)
		return nil, false
	}
	return &req, true
}

// Fail 统一错误出口：记日志 + 写错误响应。HTTP 始终 200，错误码在响应体里。
func Fail(ctx *gin.Context, err error) {
	log.LogrusObj.Infoln(err)
	ctx.JSON(http.StatusOK, ErrorResponse(ctx, err))
}

// OK 统一成功出口。
func OK(ctx *gin.Context, data interface{}) {
	ctx.JSON(http.StatusOK, ctl.RespSuccess(ctx, data))
}
