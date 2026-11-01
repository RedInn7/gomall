package promo

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/internal/product"
	"github.com/RedInn7/gomall/internal/shared/response"
)

// repriceItems 按 product_id 反查单价与类目，与下单侧 resolveProductPricing 同一口径。
func repriceItems(ctx context.Context, in []PromoCalculateItem) ([]CartItem, error) {
	pdao := product.NewProductDao(ctx)
	items := make([]CartItem, 0, len(in))
	for _, it := range in {
		p, err := pdao.GetProductById(uint(it.ProductID))
		if err != nil || p == nil {
			return nil, fmt.Errorf("商品不存在 product=%d", it.ProductID)
		}
		if !p.OnSale {
			return nil, product.ErrProductNotOnSale
		}
		cents, ok := p.UnitCents()
		if !ok {
			return nil, fmt.Errorf("商品定价非法 product=%d", it.ProductID)
		}
		items = append(items, CartItem{
			ProductID:  it.ProductID,
			CategoryID: int64(p.CategoryID),
			UnitCents:  cents,
			Quantity:   it.Quantity,
		})
	}
	return items, nil
}

func parseRuleID(c *gin.Context) (uint, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		c.JSON(http.StatusOK, response.ErrorResponse(c, errors.New("invalid rule id")))
		return 0, false
	}
	return uint(id), true
}

// PromoCalculateHandler 公开接口（不强制登录）：
// 前端把当前购物车快照传过来，引擎返回最优一条规则与减免金额。
func PromoCalculateHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		req, ok := response.Bind[PromoCalculateReq](c)
		if !ok {
			return
		}
		items, err := repriceItems(c.Request.Context(), req.Items)
		if err != nil {
			response.Fail(c, err)
			return
		}
		// 预算不够的规则在引擎里已经剔除，这里不会再出现"预算用尽"错误。
		resp, err := GetPromoSrv().CalculateBestDiscount(c.Request.Context(), items)
		if err != nil {
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
		id, ok := parseRuleID(c)
		if !ok {
			return
		}
		if err := GetPromoSrv().StopRule(c.Request.Context(), id); err != nil {
			response.Fail(c, err)
			return
		}
		response.OK(c, gin.H{"id": id, "stopped": true})
	}
}

// AdminUpdatePromoRuleHandler admin 修改草稿规则
func AdminUpdatePromoRuleHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := parseRuleID(c)
		if !ok {
			return
		}
		req, ok := response.Bind[PromoRuleCreateReq](c)
		if !ok {
			return
		}
		r, err := GetPromoSrv().UpdateRule(c.Request.Context(), id, req)
		if err != nil {
			response.Fail(c, err)
			return
		}
		response.OK(c, r)
	}
}

// AdminActivatePromoRuleHandler admin 启用草稿规则
func AdminActivatePromoRuleHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := parseRuleID(c)
		if !ok {
			return
		}
		if err := GetPromoSrv().ActivateRule(c.Request.Context(), id); err != nil {
			response.Fail(c, err)
			return
		}
		response.OK(c, gin.H{"id": id, "active": true})
	}
}
