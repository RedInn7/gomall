package admin

import (
	"errors"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/internal/clearing"
	"github.com/RedInn7/gomall/internal/shared/response"
	"github.com/RedInn7/gomall/internal/user"
	"github.com/RedInn7/gomall/service/search"
)

func AdminListUsersHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
		size, _ := strconv.Atoi(c.DefaultQuery("page_size", "50"))
		resp, err := GetAdminSrv().ListAllUsers(c.Request.Context(), page, size)
		if err != nil {
			response.Fail(c, err)
			return
		}
		response.OK(c, resp)
	}
}

// AdminListPaymentAnomaliesHandler 分页查询已被系统隔离、等待人工处置的外部异常款。
func AdminListPaymentAnomaliesHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
		pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "50"))
		orderID, err := optionalUintQuery(c.Query("order_id"))
		if err != nil {
			response.Fail(c, err)
			return
		}
		result, err := clearing.ListPaymentAnomalies(c.Request.Context(), clearing.PaymentAnomalyListFilter{
			Page: page, PageSize: pageSize, Status: c.Query("status"), Channel: c.Query("channel"),
			Reason: c.Query("reason"), OrderID: orderID,
		})
		if err != nil {
			response.Fail(c, err)
			return
		}
		response.OK(c, result)
	}
}

// AdminGetPaymentAnomalyHandler 返回异常款完整详情及不可变状态变更历史。
func AdminGetPaymentAnomalyHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := requiredUintParam(c.Param("id"))
		if err != nil {
			response.Fail(c, err)
			return
		}
		result, err := clearing.GetPaymentAnomaly(c.Request.Context(), id)
		if err != nil {
			response.Fail(c, err)
			return
		}
		response.OK(c, result)
	}
}

// AdminRecordPaymentAnomalyTransitionHandler 只记录人工复核/外部退款结果，不发起退款或转账。
func AdminRecordPaymentAnomalyTransitionHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := requiredUintParam(c.Param("id"))
		if err != nil {
			response.Fail(c, err)
			return
		}
		req, ok := response.Bind[clearing.PaymentAnomalyTransitionRequest](c)
		if !ok {
			return
		}
		result, err := clearing.RecordPaymentAnomalyTransition(c.Request.Context(), id, *req)
		if err != nil {
			response.Fail(c, err)
			return
		}
		response.OK(c, result)
	}
}

func requiredUintParam(raw string) (uint, error) {
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || value == 0 {
		return 0, errors.New("invalid id")
	}
	return uint(value), nil
}

func optionalUintQuery(raw string) (uint, error) {
	if raw == "" {
		return 0, nil
	}
	return requiredUintParam(raw)
}

func AdminPromoteUserHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		type promoteReq struct {
			UserId uint `json:"user_id" form:"user_id" binding:"required"`
			// Role 目标角色（user/merchant/admin），缺省 admin 兼容旧调用方
			Role string `json:"role" form:"role"`
		}
		req, ok := response.Bind[promoteReq](c)
		if !ok {
			return
		}
		if req.Role == "" {
			req.Role = user.RoleAdmin
		}
		if err := GetAdminSrv().PromoteUser(c.Request.Context(), req.UserId, req.Role); err != nil {
			response.Fail(c, err)
			return
		}
		response.OK(c, gin.H{"promoted": req.UserId, "role": req.Role})
	}
}

// BootstrapAdminHandler 仅当系统无 admin 时可用
func BootstrapAdminHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := GetAdminSrv().BootstrapPromoteSelf(c.Request.Context()); err != nil {
			response.Fail(c, err)
			return
		}
		response.OK(c, gin.H{"ok": true})
	}
}

// AdminBackfillProductIndexHandler 把 DB 中所有 product 灌一遍到 ES (一次性运维操作)
func AdminBackfillProductIndexHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		batch, _ := strconv.Atoi(c.DefaultQuery("batch", "200"))
		n, err := search.BackfillFromDB(c.Request.Context(), batch)
		if err != nil {
			response.Fail(c, err)
			return
		}
		response.OK(c, gin.H{"indexed": n})
	}
}
