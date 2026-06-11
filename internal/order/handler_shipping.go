package order

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/internal/shared/response"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
	"github.com/RedInn7/gomall/pkg/utils/log"
)

// ShipOrderHandler 商家发货：WaitShip -> WaitReceive。
// 当前路由挂 admin RBAC，merchant 角色落地后切换。
func ShipOrderHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var req ShipOrderReq
		if err := ctx.ShouldBind(&req); err != nil {
			log.LogrusObj.Infoln(err)
			ctx.JSON(http.StatusOK, response.ErrorResponse(ctx, err))
			return
		}
		if err := GetShippingSrv().ShipOrder(ctx.Request.Context(), req.OrderNum, req.TrackingNo, req.Carrier); err != nil {
			log.LogrusObj.Infoln(err)
			ctx.JSON(http.StatusOK, response.ErrorResponse(ctx, err))
			return
		}
		ctx.JSON(http.StatusOK, ctl.RespSuccess(ctx, gin.H{"order_num": req.OrderNum}))
	}
}

// ConfirmReceiveHandler 用户确认收货：WaitReceive -> Completed。
func ConfirmReceiveHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var req ConfirmReceiveReq
		if err := ctx.ShouldBind(&req); err != nil {
			log.LogrusObj.Infoln(err)
			ctx.JSON(http.StatusOK, response.ErrorResponse(ctx, err))
			return
		}
		if err := GetShippingSrv().ConfirmReceive(ctx.Request.Context(), req.OrderNum); err != nil {
			log.LogrusObj.Infoln(err)
			ctx.JSON(http.StatusOK, response.ErrorResponse(ctx, err))
			return
		}
		ctx.JSON(http.StatusOK, ctl.RespSuccess(ctx, gin.H{"order_num": req.OrderNum}))
	}
}
