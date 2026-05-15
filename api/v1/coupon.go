package v1

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/pkg/utils/ctl"
	"github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/service"
	"github.com/RedInn7/gomall/types"
)

func CreateCouponBatchHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		var req types.CouponBatchCreateReq
		if err := c.ShouldBind(&req); err != nil {
			log.LogrusObj.Infoln(err)
			c.JSON(http.StatusOK, ErrorResponse(c, err))
			return
		}
		resp, err := service.GetCouponSrv().CreateBatch(c.Request.Context(), &req)
		if err != nil {
			c.JSON(http.StatusOK, ErrorResponse(c, err))
			return
		}
		c.JSON(http.StatusOK, ctl.RespSuccess(c, resp))
	}
}

func ListCouponBatchHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		resp, err := service.GetCouponSrv().ListActiveBatches(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusOK, ErrorResponse(c, err))
			return
		}
		c.JSON(http.StatusOK, ctl.RespSuccess(c, resp))
	}
}

func ClaimCouponHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		var req types.CouponClaimReq
		if err := c.ShouldBind(&req); err != nil {
			log.LogrusObj.Infoln(err)
			c.JSON(http.StatusOK, ErrorResponse(c, err))
			return
		}
		resp, err := service.GetCouponSrv().Claim(c.Request.Context(), req.Mode, req.BatchId)
		if err != nil {
			c.JSON(http.StatusOK, ErrorResponse(c, err))
			return
		}
		c.JSON(http.StatusOK, ctl.RespSuccess(c, resp))
	}
}

func ListMyCouponHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		var req types.CouponListReq
		if err := c.ShouldBind(&req); err != nil {
			log.LogrusObj.Infoln(err)
			c.JSON(http.StatusOK, ErrorResponse(c, err))
			return
		}
		resp, err := service.GetCouponSrv().ListMyCoupons(c.Request.Context(), req.Status)
		if err != nil {
			c.JSON(http.StatusOK, ErrorResponse(c, err))
			return
		}
		c.JSON(http.StatusOK, ctl.RespSuccess(c, resp))
	}
}
