package promo

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/RedInn7/gomall/pkg/e"
	"github.com/RedInn7/gomall/repository/db/dao"
	"github.com/RedInn7/gomall/service/events"
)

var (
	promoSrvIns  *PromoSrv
	promoSrvOnce sync.Once
)

// PromoSrv 满减 / 阶梯折扣引擎。无状态，全局单例。
type PromoSrv struct{}

func GetPromoSrv() *PromoSrv {
	promoSrvOnce.Do(func() { promoSrvIns = &PromoSrv{} })
	return promoSrvIns
}

// ----------------------------------------------------------------------
// 纯计算逻辑（不依赖 DB / context，便于单测）
// ----------------------------------------------------------------------

// CartItem 引擎只关心三件事：商品 id、类目 id、单价、数量。
// 与外部 model.Order / model.Cart 解耦，避免在 service/promo 里反向依赖订单包。
type CartItem struct {
	ProductID  int64
	CategoryID int64
	UnitCents  int64
	Quantity   int64
}

// SubtotalCents 计算购物车原价合计（分）。
func SubtotalCents(items []CartItem) int64 {
	var sum int64
	for _, it := range items {
		sum += it.UnitCents * it.Quantity
	}
	return sum
}

// PickBestPromoRule 在一组候选规则里挑出"用户能减得最多"的那一条。
//
//	全场规则     - 用购物车原价 vs Threshold 判断；折扣 / 减额作用于全部金额；
//	类目级规则   - 仅作用于该类目的子集；按子集合计判断 Threshold；
//	商品级规则   - 仅作用于该商品；按该商品行的小计判断 Threshold；
//
// 没有任何规则适用时返回 (nil, 0)。
//
// 业务规则：一笔订单只能用一条 PromoRule，不叠加（与 Coupon 不同）。
// 取舍：在所有"达到门槛"的候选里取"折扣金额最大"。这是给消费者侧的"最优"，
// 平台侧成本反而最高 —— 但这是行业普遍做法，不偷偷给用户用较小那条。
func PickBestPromoRule(items []CartItem, rules []*PromoRule) (*PromoRule, int64) {
	var best *PromoRule
	var bestDiscount int64
	for _, r := range rules {
		base := applicableSubtotal(items, r)
		if base < r.ThresholdCents {
			continue
		}
		d := computeDiscountOnBase(base, r)
		if d <= 0 {
			continue
		}
		if d > bestDiscount {
			best = r
			bestDiscount = d
		}
	}
	return best, bestDiscount
}

// applicableSubtotal 计算规则作用范围内的子集合计金额。
func applicableSubtotal(items []CartItem, r *PromoRule) int64 {
	var sum int64
	for _, it := range items {
		if itemMatchesRule(it, r) {
			sum += it.UnitCents * it.Quantity
		}
	}
	return sum
}

func itemMatchesRule(it CartItem, r *PromoRule) bool {
	switch r.Scope {
	case PromoScopeAll:
		return true
	case PromoScopeCategory:
		return it.CategoryID == r.ScopeRefID
	case PromoScopeProduct:
		return it.ProductID == r.ScopeRefID
	default:
		return false
	}
}

// computeDiscountOnBase 在已通过门槛校验的 base 金额上计算减免（分）。
//   - 满减：直接减 DiscountCents，且不允许减成负数（封顶在 base）
//   - 满折扣：base * (1 - bps/base)，向下取整保平台
func computeDiscountOnBase(base int64, r *PromoRule) int64 {
	switch r.RuleType {
	case PromoRuleTypeAmount:
		if r.DiscountCents > base {
			return base
		}
		return r.DiscountCents
	case PromoRuleTypeDiscount:
		if r.DiscountBps <= 0 || r.DiscountBps >= PromoDiscountBpsBase {
			return 0
		}
		// 折扣金额 = base * (base_bps - discount_bps) / base_bps
		num := base * int64(PromoDiscountBpsBase-r.DiscountBps)
		return num / int64(PromoDiscountBpsBase)
	default:
		return 0
	}
}

// buildReason 给客服 / 用户的可解释文案。所有金额一律展示为元（保留两位）。
func buildReason(r *PromoRule, discount int64) string {
	switch r.RuleType {
	case PromoRuleTypeAmount:
		return fmt.Sprintf("%s — 减 %s 元", r.Name, centsToYuan(discount))
	case PromoRuleTypeDiscount:
		discPct := float64(PromoDiscountBpsBase-r.DiscountBps) / 100.0
		return fmt.Sprintf("%s — 立省 %.1f%% 共 %s 元", r.Name, discPct, centsToYuan(discount))
	default:
		return r.Name
	}
}

func centsToYuan(c int64) string {
	sign := ""
	if c < 0 {
		sign = "-"
		c = -c
	}
	yuan := c / 100
	cents := c % 100
	return fmt.Sprintf("%s%d.%02d", sign, yuan, cents)
}

// ----------------------------------------------------------------------
// 业务编排（拉规则 / 算最优 / 落库 / 写 outbox）
// ----------------------------------------------------------------------

// CalculateBestDiscount 给前端展示用。不写预算、不落库，纯只读。
// 调用方：结算页加载、购物车实时悬浮提示。
//
// SLO：calculate p99 < 50ms（实际 DB 一次查询 + 内存循环）。
func (s *PromoSrv) CalculateBestDiscount(ctx context.Context, items []CartItem) (*PromoApplyResp, error) {
	resp := &PromoApplyResp{
		OriginalCents: SubtotalCents(items),
		FinalCents:    SubtotalCents(items),
	}
	if len(items) == 0 {
		return resp, nil
	}

	categoryIDs := uniqueCategoryIDs(items)
	productIDs := uniqueProductIDs(items)

	rules, err := NewPromoDao(ctx).
		ListActiveForCart(time.Now(), categoryIDs, productIDs)
	if err != nil {
		return nil, err
	}

	best, discount := PickBestPromoRule(items, rules)
	if best == nil || discount <= 0 {
		return resp, nil
	}
	resp.RuleID = best.ID
	resp.RuleName = best.Name
	resp.ThresholdCents = best.ThresholdCents
	resp.DiscountCents = discount
	resp.FinalCents = resp.OriginalCents - discount
	resp.Reason = buildReason(best, discount)
	return resp, nil
}

func uniqueCategoryIDs(items []CartItem) []int64 {
	seen := map[int64]struct{}{}
	out := make([]int64, 0, len(items))
	for _, it := range items {
		if it.CategoryID == 0 {
			continue
		}
		if _, ok := seen[it.CategoryID]; ok {
			continue
		}
		seen[it.CategoryID] = struct{}{}
		out = append(out, it.CategoryID)
	}
	return out
}

func uniqueProductIDs(items []CartItem) []int64 {
	seen := map[int64]struct{}{}
	out := make([]int64, 0, len(items))
	for _, it := range items {
		if it.ProductID == 0 {
			continue
		}
		if _, ok := seen[it.ProductID]; ok {
			continue
		}
		seen[it.ProductID] = struct{}{}
		out = append(out, it.ProductID)
	}
	return out
}

// ApplyDiscountInTx 在下单事务内调。
//
//	入参 tx：业务方持有的 *gorm.DB 事务句柄
//	返回 ErrPromoBudgetExhausted 时上层应回滚整笔事务并落 80003。
//
// 同时写 outbox promo.applied 事件，便于：
//   - 财务系统统计每条规则的真实补贴成本（DB 与 outbox 双写校验）
//   - 商家看板展示"本周通过 X 规则带来的销量"
//   - 风控审计"是否存在异常高频命中"
func (s *PromoSrv) ApplyDiscountInTx(tx *gorm.DB, orderID, ruleID uint, discountCents int64) error {
	if ruleID == 0 || discountCents <= 0 {
		return nil
	}
	if err := NewPromoDaoByDB(tx).AtomicConsumeBudget(tx, ruleID, discountCents); err != nil {
		return err
	}
	return dao.NewOutboxDaoByDB(tx).Insert(
		"promo", "PromoApplied", "promo.applied", orderID,
		events.PromoApplied{
			OrderID:       orderID,
			RuleID:        ruleID,
			DiscountCents: discountCents,
		},
	)
}

// ReleaseDiscount 关单 / 退款时调，把预算退还、并落 promo.released 事件。
// 入参 reason：cancel / refund / manual，便于下游统计补贴回收来源。
//
// 业务约束：不要求 ruleID 当前仍在 active —— 即使规则已 stop，也允许退还，
// 否则 stop 后未关单订单的预算会被错算掉。
func (s *PromoSrv) ReleaseDiscount(ctx context.Context, orderID, ruleID uint, discountCents int64, reason string) error {
	if ruleID == 0 || discountCents <= 0 {
		return nil
	}
	db := NewPromoDao(ctx).DB
	return db.Transaction(func(tx *gorm.DB) error {
		if err := NewPromoDaoByDB(tx).RestoreBudget(tx, ruleID, discountCents); err != nil {
			return err
		}
		return dao.NewOutboxDaoByDB(tx).Insert(
			"promo", "PromoReleased", "promo.released", orderID,
			events.PromoReleased{
				OrderID:       orderID,
				RuleID:        ruleID,
				DiscountCents: discountCents,
				Reason:        reason,
			},
		)
	})
}

// ----------------------------------------------------------------------
// admin 后台
// ----------------------------------------------------------------------

func (s *PromoSrv) CreateRule(ctx context.Context, req *PromoRuleCreateReq) (*PromoRule, error) {
	if !req.EndAt.After(req.StartAt) {
		return nil, errors.New("end_at 必须晚于 start_at")
	}
	if req.RuleType == PromoRuleTypeAmount && req.DiscountCents <= 0 {
		return nil, errors.New("满减规则必须设置 discount_cents")
	}
	if req.RuleType == PromoRuleTypeDiscount {
		if req.DiscountBps <= 0 || req.DiscountBps >= PromoDiscountBpsBase {
			return nil, errors.New("折扣 bps 必须落在 (0, 10000) 内")
		}
	}
	r := &PromoRule{
		Name:             req.Name,
		RuleType:         req.RuleType,
		Scope:            req.Scope,
		ScopeRefID:       req.ScopeRefID,
		ThresholdCents:   req.ThresholdCents,
		DiscountCents:    req.DiscountCents,
		DiscountBps:      req.DiscountBps,
		DailyBudgetCents: req.DailyBudgetCents,
		StartAt:          req.StartAt,
		EndAt:            req.EndAt,
		Status:           PromoStatusActive,
	}
	if err := NewPromoDao(ctx).Create(r); err != nil {
		return nil, err
	}
	return r, nil
}

func (s *PromoSrv) ListRules(ctx context.Context) ([]*PromoRule, error) {
	return NewPromoDao(ctx).ListAll()
}

func (s *PromoSrv) StopRule(ctx context.Context, id uint) error {
	return NewPromoDao(ctx).Stop(id)
}

// 让 e 包能被 import：占位避免 unused import 报错
var _ = e.PromoBudgetExhausted
