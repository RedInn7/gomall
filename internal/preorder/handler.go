package preorder

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/internal/shared/response"
	"github.com/RedInn7/gomall/pkg/e"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
	"github.com/RedInn7/gomall/pkg/utils/log"
)

// PreorderShowHandler 公共预售信息展示。
// 路由：GET /preorder/:productID
// 不校验登录，返回 deposit / final / not_started / forfeited 四态。
func PreorderShowHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		pid, ok := parseUintParam(ctx, "productID")
		if !ok {
			return
		}
		resp, err := GetPreorderSrv().ShowPreorder(ctx.Request.Context(), pid)
		if err != nil {
			log.LogrusObj.Infoln(err)
			ctx.JSON(http.StatusOK, response.ErrorResponse(ctx, err))
			return
		}
		ctx.JSON(http.StatusOK, ctl.RespSuccess(ctx, resp))
	}
}

// PreorderDepositHandler 付定金。
// 路由：POST /preorder/:productID/deposit
// body: { boss_id, address_id, key }
func PreorderDepositHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		pid, ok := parseUintParam(ctx, "productID")
		if !ok {
			return
		}
		var req PreorderDepositReq
		if err := ctx.ShouldBind(&req); err != nil {
			log.LogrusObj.Infoln(err)
			ctx.JSON(http.StatusOK, response.ErrorResponse(ctx, err))
			return
		}
		req.ProductID = pid

		resp, err := GetPreorderSrv().PayDeposit(ctx.Request.Context(), &req)
		if err != nil {
			respondPreorderErr(ctx, err)
			return
		}
		ctx.JSON(http.StatusOK, ctl.RespSuccess(ctx, resp))
	}
}

// PreorderFinalHandler 付尾款。
// 路由：POST /preorder/:orderID/final
// body: { key }
func PreorderFinalHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		oid, ok := parseUintParam(ctx, "orderID")
		if !ok {
			return
		}
		var req PreorderFinalReq
		if err := ctx.ShouldBind(&req); err != nil {
			log.LogrusObj.Infoln(err)
			ctx.JSON(http.StatusOK, response.ErrorResponse(ctx, err))
			return
		}
		req.OrderID = oid

		resp, err := GetPreorderSrv().PayFinal(ctx.Request.Context(), &req)
		if err != nil {
			respondPreorderErr(ctx, err)
			return
		}
		ctx.JSON(http.StatusOK, ctl.RespSuccess(ctx, resp))
	}
}

// PreorderCancelHandler 定金期内取消订单（全额退款）。
// 路由：POST /preorder/:orderID/cancel
// body: { key }
func PreorderCancelHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		oid, ok := parseUintParam(ctx, "orderID")
		if !ok {
			return
		}
		var req PreorderCancelReq
		if err := ctx.ShouldBind(&req); err != nil {
			log.LogrusObj.Infoln(err)
			ctx.JSON(http.StatusOK, response.ErrorResponse(ctx, err))
			return
		}
		req.OrderID = oid

		resp, err := GetPreorderSrv().CancelPreorderInDepositWindow(ctx.Request.Context(), &req)
		if err != nil {
			respondPreorderErr(ctx, err)
			return
		}
		ctx.JSON(http.StatusOK, ctl.RespSuccess(ctx, resp))
	}
}

// parseUintParam URI 段位转 uint，失败直接 400 + 日志。
func parseUintParam(ctx *gin.Context, name string) (uint, bool) {
	raw := ctx.Param(name)
	if raw == "" {
		ctx.JSON(http.StatusOK, ctl.RespError(ctx, errMissingPathParam, "缺少路径参数 "+name, e.InvalidParams))
		return 0, false
	}
	n, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		ctx.JSON(http.StatusOK, ctl.RespError(ctx, err, "路径参数非法 "+name, e.InvalidParams))
		return 0, false
	}
	return uint(n), true
}

var errMissingPathParam = preorderHandlerErr("missing path param")

type preorderHandlerErr string

func (p preorderHandlerErr) Error() string { return string(p) }

// respondPreorderErr 把业务码透出给客户端，让前端按 82xxx 拉对应文案。
func respondPreorderErr(ctx *gin.Context, err error) {
	log.LogrusObj.Infoln(err)
	code := CodeOf(err)
	if code == e.SUCCESS || code == e.ERROR {
		ctx.JSON(http.StatusOK, response.ErrorResponse(ctx, err))
		return
	}
	ctx.JSON(http.StatusOK, ctl.RespError(ctx, err, e.GetMsg(code), code))
}
