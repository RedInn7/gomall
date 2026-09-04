package clearing

import (
	"errors"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/internal/shared/response"
)

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
		result, err := ListPaymentAnomalies(c.Request.Context(), PaymentAnomalyListFilter{
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
		result, err := GetPaymentAnomaly(c.Request.Context(), id)
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
		req, ok := response.Bind[PaymentAnomalyTransitionRequest](c)
		if !ok {
			return
		}
		result, err := RecordPaymentAnomalyTransition(c.Request.Context(), id, *req)
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
