package v1

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/pkg/e"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
	"github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/service"
	"github.com/RedInn7/gomall/types"
)

// PromoCalculateHandler 公开接口（不强制登录）：
// 前端把当前购物车快照传过来，引擎返回最优一条规则与减免金额。
func PromoCalculateHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		var req types.PromoCalculateReq
		if err := c.ShouldBind(&req); err != nil {
			log.LogrusObj.Infoln(err)
			c.JSON(http.StatusOK, ErrorResponse(c, err))
			return
		}
		items := make([]service.CartItem, 0, len(req.Items))
		for _, it := range req.Items {
			items = append(items, service.CartItem{
				ProductID:  it.ProductID,
				CategoryID: it.CategoryID,
				UnitCents:  it.UnitCents,
				Quantity:   it.Quantity,
			})
		}
		resp, err := service.GetPromoSrv().CalculateBestDiscount(c.Request.Context(), items)
		if err != nil {
			if errors.Is(err, service.ErrPromoBudgetExhausted) {
				c.JSON(http.StatusOK, ctl.RespError(c, err,
					"满减预算已用完", e.PromoBudgetExhausted))
				return
			}
			c.JSON(http.StatusOK, ErrorResponse(c, err))
			return
		}
		c.JSON(http.StatusOK, ctl.RespSuccess(c, resp))
	}
}

// AdminListPromoRulesHandler admin 列规则
func AdminListPromoRulesHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		rows, err := service.GetPromoSrv().ListRules(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusOK, ErrorResponse(c, err))
			return
		}
		c.JSON(http.StatusOK, ctl.RespSuccess(c, rows))
	}
}

// AdminCreatePromoRuleHandler admin 创建规则
func AdminCreatePromoRuleHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		var req types.PromoRuleCreateReq
		if err := c.ShouldBind(&req); err != nil {
			log.LogrusObj.Infoln(err)
			c.JSON(http.StatusOK, ErrorResponse(c, err))
			return
		}
		r, err := service.GetPromoSrv().CreateRule(c.Request.Context(), &req)
		if err != nil {
			c.JSON(http.StatusOK, ErrorResponse(c, err))
			return
		}
		c.JSON(http.StatusOK, ctl.RespSuccess(c, r))
	}
}

// AdminStopPromoRuleHandler admin 停规则
func AdminStopPromoRuleHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		idStr := c.Param("id")
		id, err := strconv.ParseUint(idStr, 10, 64)
		if err != nil || id == 0 {
			c.JSON(http.StatusOK, ErrorResponse(c, errors.New("invalid rule id")))
			return
		}
		if err := service.GetPromoSrv().StopRule(c.Request.Context(), uint(id)); err != nil {
			c.JSON(http.StatusOK, ErrorResponse(c, err))
			return
		}
		c.JSON(http.StatusOK, ctl.RespSuccess(c, gin.H{"id": id, "stopped": true}))
	}
}
