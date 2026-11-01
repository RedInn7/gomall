package promo

import "time"

// PromoCalculateItem 计算入参里一行购物车快照。
// 单价、类目一律按 product_id 从商品表反查，客户端传的 unit_cents / category_id 不参与计算，
// 否则展示出来的折扣和下单时服务端按真实价格算出来的对不上。
type PromoCalculateItem struct {
	ProductID  int64 `json:"product_id" binding:"required"`
	CategoryID int64 `json:"category_id"`
	UnitCents  int64 `json:"unit_cents"`
	Quantity   int64 `json:"quantity" binding:"required,min=1,max=99"`
}

// PromoCalculateReq 只收一行：一张订单只有一个商品，满减门槛按单笔订单算，
// 多行"凑单"试算会展示一个真实下单拿不到的折扣。
type PromoCalculateReq struct {
	Items []PromoCalculateItem `json:"items" binding:"required,min=1,max=1,dive"`
}

// PromoApplyResp 计算结果。
//
//	RuleID=0 表示没有任何规则适用，前端应展示原价；
//	DiscountCents 是本笔订单可减金额（单位：分），订单实付 = 原价 - DiscountCents；
//	Reason 是给客服 / 用户看的可解释文案。
type PromoApplyResp struct {
	RuleID         uint   `json:"rule_id"`
	RuleName       string `json:"rule_name"`
	OriginalCents  int64  `json:"original_cents"`
	DiscountCents  int64  `json:"discount_cents"`
	FinalCents     int64  `json:"final_cents"`
	Reason         string `json:"reason"`
	ThresholdCents int64  `json:"threshold_cents"`
	// Candidates 预算还够的全部规则，按减免从大到小，第 0 个就是 RuleID 那条。只给下单链路降级用。
	Candidates []PromoCandidate `json:"-"`
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
