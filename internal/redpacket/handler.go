package redpacket

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/internal/shared/response"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
	"github.com/RedInn7/gomall/pkg/utils/log"
)

// CreateRedPacketHandler 发红包
func CreateRedPacketHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		var req RedPacketCreateReq
		if err := c.ShouldBind(&req); err != nil {
			log.LogrusObj.Infoln(err)
			c.JSON(http.StatusOK, response.ErrorResponse(c, err))
			return
		}
		resp, err := GetRedPacketSrv().Create(c.Request.Context(), &req)
		if err != nil {
			log.LogrusObj.Infoln(err)
			c.JSON(http.StatusOK, response.ErrorResponse(c, err))
			return
		}
		c.JSON(http.StatusOK, ctl.RespSuccess(c, resp))
	}
}

// ClaimRedPacketHandler 抢红包
func ClaimRedPacketHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		var req RedPacketClaimReq
		if err := c.ShouldBind(&req); err != nil {
			log.LogrusObj.Infoln(err)
			c.JSON(http.StatusOK, response.ErrorResponse(c, err))
			return
		}
		resp, err := GetRedPacketSrv().Claim(c.Request.Context(), &req)
		if err != nil {
			log.LogrusObj.Infoln(err)
			c.JSON(http.StatusOK, response.ErrorResponse(c, err))
			return
		}
		c.JSON(http.StatusOK, ctl.RespSuccess(c, resp))
	}
}

// ShowRedPacketHandler 红包详情 + 领取明细
func ShowRedPacketHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		var req RedPacketShowReq
		if err := c.ShouldBind(&req); err != nil {
			log.LogrusObj.Infoln(err)
			c.JSON(http.StatusOK, response.ErrorResponse(c, err))
			return
		}
		resp, err := GetRedPacketSrv().Show(c.Request.Context(), &req)
		if err != nil {
			log.LogrusObj.Infoln(err)
			c.JSON(http.StatusOK, response.ErrorResponse(c, err))
			return
		}
		c.JSON(http.StatusOK, ctl.RespSuccess(c, resp))
	}
}

// ListMyRedPacketsHandler 我发出过的红包列表
func ListMyRedPacketsHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		var req RedPacketListReq
		if err := c.ShouldBind(&req); err != nil {
			log.LogrusObj.Infoln(err)
			c.JSON(http.StatusOK, response.ErrorResponse(c, err))
			return
		}
		resp, err := GetRedPacketSrv().ListMine(c.Request.Context(), &req)
		if err != nil {
			log.LogrusObj.Infoln(err)
			c.JSON(http.StatusOK, response.ErrorResponse(c, err))
			return
		}
		c.JSON(http.StatusOK, ctl.RespSuccess(c, resp))
	}
}
