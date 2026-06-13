package promo

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/internal/shared/response"
	"github.com/RedInn7/gomall/pkg/e"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
)

// PromoCalculateHandler 公开接口（不强制登录）：
// 前端把当前购物车快照传过来，引擎返回最优一条规则与减免金额。
func PromoCalculateHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		req, ok := response.Bind[PromoCalculateReq](c)
		if !ok {
			return
		}
		items := make([]CartItem, 0, len(req.Items))
		for _, it := range req.Items {
			items = append(items, CartItem{
				ProductID:  it.ProductID,
				CategoryID: it.CategoryID,
				UnitCents:  it.UnitCents,
				Quantity:   it.Quantity,
			})
		}
		resp, err := GetPromoSrv().CalculateBestDiscount(c.Request.Context(), items)
		if err != nil {
			if errors.Is(err, ErrPromoBudgetExhausted) {
				c.JSON(http.StatusOK, ctl.RespError(c, err,
					"满减预算已用完", e.PromoBudgetExhausted))
				return
			}
			response.Fail(c, err)
			return
		}
		response.OK(c, resp)
	}
}

// AdminListPromoRulesHandler admin 列规则
func AdminListPromoRulesHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		rows, err := GetPromoSrv().ListRules(c.Request.Context())
		if err != nil {
			response.Fail(c, err)
			return
		}
		response.OK(c, rows)
	}
}

// AdminCreatePromoRuleHandler admin 创建规则
func AdminCreatePromoRuleHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		req, ok := response.Bind[PromoRuleCreateReq](c)
		if !ok {
			return
		}
		r, err := GetPromoSrv().CreateRule(c.Request.Context(), req)
		if err != nil {
			response.Fail(c, err)
			return
		}
		response.OK(c, r)
	}
}

// AdminStopPromoRuleHandler admin 停规则
func AdminStopPromoRuleHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		idStr := c.Param("id")
		id, err := strconv.ParseUint(idStr, 10, 64)
		if err != nil || id == 0 {
			c.JSON(http.StatusOK, response.ErrorResponse(c, errors.New("invalid rule id")))
			return
		}
		if err := GetPromoSrv().StopRule(c.Request.Context(), uint(id)); err != nil {
			response.Fail(c, err)
			return
		}
		response.OK(c, gin.H{"id": id, "stopped": true})
	}
}
