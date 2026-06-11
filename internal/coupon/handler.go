package coupon

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/internal/shared/response"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
	"github.com/RedInn7/gomall/pkg/utils/log"
)

func CreateCouponBatchHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		var req CouponBatchCreateReq
		if err := c.ShouldBind(&req); err != nil {
			log.LogrusObj.Infoln(err)
			c.JSON(http.StatusOK, response.ErrorResponse(c, err))
			return
		}
		resp, err := GetCouponSrv().CreateBatch(c.Request.Context(), &req)
		if err != nil {
			c.JSON(http.StatusOK, response.ErrorResponse(c, err))
			return
		}
		c.JSON(http.StatusOK, ctl.RespSuccess(c, resp))
	}
}

func ListCouponBatchHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		resp, err := GetCouponSrv().ListActiveBatches(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusOK, response.ErrorResponse(c, err))
			return
		}
		c.JSON(http.StatusOK, ctl.RespSuccess(c, resp))
	}
}

func ClaimCouponHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		var req CouponClaimReq
		if err := c.ShouldBind(&req); err != nil {
			log.LogrusObj.Infoln(err)
			c.JSON(http.StatusOK, response.ErrorResponse(c, err))
			return
		}
		resp, err := GetCouponSrv().Claim(c.Request.Context(), req.Mode, req.BatchId)
		if err != nil {
			c.JSON(http.StatusOK, response.ErrorResponse(c, err))
			return
		}
		c.JSON(http.StatusOK, ctl.RespSuccess(c, resp))
	}
}

func ListMyCouponHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		var req CouponListReq
		if err := c.ShouldBind(&req); err != nil {
			log.LogrusObj.Infoln(err)
			c.JSON(http.StatusOK, response.ErrorResponse(c, err))
			return
		}
		resp, err := GetCouponSrv().ListMyCoupons(c.Request.Context(), req.Status)
		if err != nil {
			c.JSON(http.StatusOK, response.ErrorResponse(c, err))
			return
		}
		c.JSON(http.StatusOK, ctl.RespSuccess(c, resp))
	}
}
