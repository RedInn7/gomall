package coupon

import (
	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/internal/shared/response"
)

func CreateCouponBatchHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		req, ok := response.Bind[CouponBatchCreateReq](c)
		if !ok {
			return
		}
		resp, err := GetCouponSrv().CreateBatch(c.Request.Context(), req)
		if err != nil {
			response.Fail(c, err)
			return
		}
		response.OK(c, resp)
	}
}

func ListCouponBatchHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		resp, err := GetCouponSrv().ListActiveBatches(c.Request.Context())
		if err != nil {
			response.Fail(c, err)
			return
		}
		response.OK(c, resp)
	}
}

func ClaimCouponHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		req, ok := response.Bind[CouponClaimReq](c)
		if !ok {
			return
		}
		resp, err := GetCouponSrv().Claim(c.Request.Context(), req.Mode, req.BatchId)
		if err != nil {
			response.Fail(c, err)
			return
		}
		response.OK(c, resp)
	}
}

func ListMyCouponHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		req, ok := response.Bind[CouponListReq](c)
		if !ok {
			return
		}
		resp, err := GetCouponSrv().ListMyCoupons(c.Request.Context(), req.Status)
		if err != nil {
			response.Fail(c, err)
			return
		}
		response.OK(c, resp)
	}
}
