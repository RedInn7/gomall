package groupbuy

import (
	"errors"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/internal/shared/response"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
)

// GroupbuyCreateReq 团长发起拼团请求。
//   - target_count >= 2，建议 3 / 5
//   - 拼团价不由客户端传：取商家在商品上配置的拼团价
//   - ttl_seconds 留 0 时默认 24h
//   - key 支付密码，字段名与 paydown 一致
type GroupbuyCreateReq struct {
	ProductID   uint   `json:"product_id" binding:"required"`
	TargetCount int    `json:"target_count" binding:"required"`
	TTLSeconds  int64  `json:"ttl_seconds"`
	BossID      uint   `json:"boss_id"`
	AddressID   uint   `json:"address_id"`
	Key         string `json:"key"`
}

// GroupbuyJoinReq 加入团请求。key 为支付密码，参团即扣款。
type GroupbuyJoinReq struct {
	BossID    uint   `json:"boss_id"`
	AddressID uint   `json:"address_id"`
	Key       string `json:"key"`
}

// GroupbuyShowResp 团状态分享落地页响应。
type GroupbuyShowResp struct {
	Group   *GroupbuyGroup    `json:"group"`
	Members []*GroupbuyMember `json:"members"`
}

// GroupbuyCreateHandler 团长发起拼团。
func GroupbuyCreateHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		req, ok := response.BindJSON[GroupbuyCreateReq](c)
		if !ok {
			return
		}
		u, err := ctl.GetUserInfo(c.Request.Context())
		if err != nil {
			response.Fail(c, err)
			return
		}

		ttl := time.Duration(req.TTLSeconds) * time.Second
		resp, err := GetGroupbuySrv().CreateGroup(
			c.Request.Context(),
			u.Id, req.ProductID, req.TargetCount, ttl,
			req.BossID, req.AddressID, req.Key,
		)
		if err != nil {
			response.Fail(c, err)
			return
		}
		response.OK(c, resp)
	}
}

// GroupbuyJoinHandler 用户加入团。
func GroupbuyJoinHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		groupID, err := parseGroupID(c)
		if err != nil {
			response.Fail(c, err)
			return
		}
		// 参团要扣款，body 里必须带支付密码
		req, ok := response.BindJSON[GroupbuyJoinReq](c)
		if !ok {
			return
		}

		u, err := ctl.GetUserInfo(c.Request.Context())
		if err != nil {
			response.Fail(c, err)
			return
		}

		resp, err := GetGroupbuySrv().JoinGroup(
			c.Request.Context(), u.Id, groupID, req.BossID, req.AddressID, req.Key,
		)
		if err != nil {
			response.Fail(c, err)
			return
		}
		response.OK(c, resp)
	}
}

// GroupbuyShowHandler 分享落地页：免登录可看，给社交裂变留口。
func GroupbuyShowHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		groupID, err := parseGroupID(c)
		if err != nil {
			response.Fail(c, err)
			return
		}
		g, members, err := GetGroupbuySrv().ShowGroup(c.Request.Context(), groupID)
		if err != nil {
			response.Fail(c, err)
			return
		}
		response.OK(c, GroupbuyShowResp{Group: g, Members: members})
	}
}

// GroupbuyCloseReq 人工关团请求，reason 必填，落库审计。
type GroupbuyCloseReq struct {
	Reason string `json:"reason"`
}

// AdminCloseGroupHandler 运营 / 风控手动关团：仅进行中的团可关，关团即原路退款。
func AdminCloseGroupHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		groupID, err := parseGroupID(c)
		if err != nil {
			response.Fail(c, err)
			return
		}
		req, ok := response.BindJSON[GroupbuyCloseReq](c)
		if !ok {
			return
		}
		u, err := ctl.GetUserInfo(c.Request.Context())
		if err != nil {
			response.Fail(c, err)
			return
		}
		if err = GetGroupbuySrv().CloseGroup(c.Request.Context(), groupID, u.Id, req.Reason); err != nil {
			response.Fail(c, err)
			return
		}
		response.OK(c, nil)
	}
}

// parseGroupID 从 path 解析团 id；失败直接返参错误。
func parseGroupID(c *gin.Context) (uint, error) {
	raw := c.Param("id")
	id, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || id == 0 {
		return 0, errors.New("无效的团 id")
	}
	return uint(id), nil
}
