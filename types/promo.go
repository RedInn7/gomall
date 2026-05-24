package types

import "time"

// PromoCalculateItem 计算入参里一行购物车快照。
// 由 service 层把购物车 row 翻译过来；handler 直接转发前端入参。
type PromoCalculateItem struct {
	ProductID  int64 `json:"product_id" binding:"required"`
	CategoryID int64 `json:"category_id"`
	UnitCents  int64 `json:"unit_cents" binding:"required"`
	Quantity   int64 `json:"quantity" binding:"required,min=1"`
}

type PromoCalculateReq struct {
	Items []PromoCalculateItem `json:"items" binding:"required,min=1,dive"`
}

// PromoApplyResp 计算结果。
//   RuleID=0 表示没有任何规则适用，前端应展示原价；
//   DiscountCents 是本笔订单可减金额（单位：分），订单实付 = 原价 - DiscountCents；
//   Reason 是给客服 / 用户看的可解释文案。
type PromoApplyResp struct {
	RuleID         uint   `json:"rule_id"`
	RuleName       string `json:"rule_name"`
	OriginalCents  int64  `json:"original_cents"`
	DiscountCents  int64  `json:"discount_cents"`
	FinalCents     int64  `json:"final_cents"`
	Reason         string `json:"reason"`
	ThresholdCents int64  `json:"threshold_cents"`
}

type PromoRuleCreateReq struct {
	Name             string    `json:"name" binding:"required"`
	RuleType         int       `json:"rule_type" binding:"required,oneof=1 2"`
	Scope            int       `json:"scope" binding:"required,oneof=1 2 3"`
	ScopeRefID       int64     `json:"scope_ref_id"`
	ThresholdCents   int64     `json:"threshold_cents" binding:"required,min=1"`
	DiscountCents    int64     `json:"discount_cents"`
	DiscountBps      int       `json:"discount_bps"`
	DailyBudgetCents int64     `json:"daily_budget_cents"`
	StartAt          time.Time `json:"start_at" binding:"required"`
	EndAt            time.Time `json:"end_at" binding:"required"`
}
