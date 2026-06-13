package redpacket

import (
	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/internal/shared/response"
)

// CreateRedPacketHandler 发红包
func CreateRedPacketHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		req, ok := response.Bind[RedPacketCreateReq](c)
		if !ok {
			return
		}
		resp, err := GetRedPacketSrv().Create(c.Request.Context(), req)
		if err != nil {
			response.Fail(c, err)
			return
		}
		response.OK(c, resp)
	}
}

// ClaimRedPacketHandler 抢红包
func ClaimRedPacketHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		req, ok := response.Bind[RedPacketClaimReq](c)
		if !ok {
			return
		}
		resp, err := GetRedPacketSrv().Claim(c.Request.Context(), req)
		if err != nil {
			response.Fail(c, err)
			return
		}
		response.OK(c, resp)
	}
}

// ShowRedPacketHandler 红包详情 + 领取明细
func ShowRedPacketHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		req, ok := response.Bind[RedPacketShowReq](c)
		if !ok {
			return
		}
		resp, err := GetRedPacketSrv().Show(c.Request.Context(), req)
		if err != nil {
			response.Fail(c, err)
			return
		}
		response.OK(c, resp)
	}
}

// ListMyRedPacketsHandler 我发出过的红包列表
func ListMyRedPacketsHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		req, ok := response.Bind[RedPacketListReq](c)
		if !ok {
			return
		}
		resp, err := GetRedPacketSrv().ListMine(c.Request.Context(), req)
		if err != nil {
			response.Fail(c, err)
			return
		}
		response.OK(c, resp)
	}
}
