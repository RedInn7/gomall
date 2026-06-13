package order

import (
	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/internal/shared/response"
)

// ShipOrderHandler 商家发货：WaitShip -> WaitReceive。
// 当前路由挂 admin RBAC，merchant 角色落地后切换。
func ShipOrderHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		req, ok := response.Bind[ShipOrderReq](ctx)
		if !ok {
			return
		}
		if err := GetShippingSrv().ShipOrder(ctx.Request.Context(), req.OrderNum, req.TrackingNo, req.Carrier); err != nil {
			response.Fail(ctx, err)
			return
		}
		response.OK(ctx, gin.H{"order_num": req.OrderNum})
	}
}

// ConfirmReceiveHandler 用户确认收货：WaitReceive -> Completed。
func ConfirmReceiveHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		req, ok := response.Bind[ConfirmReceiveReq](ctx)
		if !ok {
			return
		}
		if err := GetShippingSrv().ConfirmReceive(ctx.Request.Context(), req.OrderNum); err != nil {
			response.Fail(ctx, err)
			return
		}
		response.OK(ctx, gin.H{"order_num": req.OrderNum})
	}
}
