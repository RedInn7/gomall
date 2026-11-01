package promo

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/RedInn7/gomall/internal/shared/outbox"
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
	ranked := RankPromoRules(items, rules)
	if len(ranked) == 0 {
		return nil, 0
	}
	return ranked[0].Rule, ranked[0].DiscountCents
}

// PromoCandidate 一条达到门槛的规则及其可减金额。
type PromoCandidate struct {
	Rule          *PromoRule
	DiscountCents int64
}

// RankPromoRules 所有达到门槛的规则按减免从大到小排；减免相同保持入参顺序（id 小的在前）。
// 下单时第一名预算被挤光，就按这个顺序往下试次优，而不是直接降成无折扣。
func RankPromoRules(items []CartItem, rules []*PromoRule) []PromoCandidate {
	var out []PromoCandidate
	for _, r := range rules {
		base := applicableSubtotal(items, r)
		if base < r.ThresholdCents {
			continue
		}
		if d := computeDiscountOnBase(base, r); d > 0 {
			out = append(out, PromoCandidate{Rule: r, DiscountCents: d})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].DiscountCents > out[j].DiscountCents })
	return out
}

// budgetAllows 按规则行快照判断今天的剩余预算还够不够减 discount。
// 只是试算口径，真正的防超发在 AtomicConsumeBudget 的条件 UPDATE。
func budgetAllows(r *PromoRule, discount int64, today string) bool {
	if r.DailyBudgetCents == 0 {
		return true
	}
	used := int64(0)
	if r.BudgetDate == today {
		used = r.ConsumedToday
	}
	return used+discount <= r.DailyBudgetCents
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

// CalculateBestDiscount 给前端展示、也给下单链路选规则用。不写预算、不落库，纯只读。
// 今天剩余预算已经不够的规则直接剔除，展示口径与下单时能拿到的折扣一致；
// Candidates 带上全部可用规则（含最优），下单事务里最优那条被并发挤光时按序降级。
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

	now := time.Now()
	rules, err := NewPromoDao(ctx).
		ListActiveForCart(now, categoryIDs, productIDs)
	if err != nil {
		return nil, err
	}

	today := BudgetDay(now)
	for _, c := range RankPromoRules(items, rules) {
		if budgetAllows(c.Rule, c.DiscountCents, today) {
			resp.Candidates = append(resp.Candidates, c)
		}
	}
	if len(resp.Candidates) == 0 {
		return resp, nil
	}
	best, discount := resp.Candidates[0].Rule, resp.Candidates[0].DiscountCents
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
//	返回 ErrPromoBudgetExhausted 时预算没扣、事件没写，下单侧换次优规则重试或降级为无折扣，不拒单。
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
	return outbox.NewOutboxDaoByDB(tx).Insert(
		"promo", "PromoApplied", "promo.applied", orderID,
		events.PromoApplied{
			OrderID:       orderID,
			RuleID:        ruleID,
			DiscountCents: discountCents,
		},
	)
}

// ReleaseDiscount 关单 / 退款时调，把预算退还、并落 promo.released 事件。
// 入参 budgetDay：订单扣预算的那一天（BudgetDay(order.CreatedAt)），跨天的订单只记台账不回补；
// 入参 reason：cancel / refund / manual，便于下游统计补贴回收来源。
//
// 业务约束：不要求 ruleID 当前仍在 active —— 即使规则已 stop，也允许退还，
// 否则 stop 后未关单订单的预算会被错算掉。
//
// 幂等保障：调用方是 order.cancelled / order.refunded 的 at-least-once 消费者，
// 同一订单可能重复进来。事务内先写 promo_release 台账（order_id 唯一索引 +
// ON CONFLICT DO NOTHING / ON DUPLICATE KEY），冲突即说明已释放过，
// 直接返回 nil —— 不重复回补预算、不重复发 promo.released 事件。
// 台账与事件里的 Restored 如实记录预算是否真的回补，不把"作废"记成"已退还"。
func (s *PromoSrv) ReleaseDiscount(ctx context.Context, orderID, ruleID uint, discountCents int64, budgetDay, reason string) error {
	if ruleID == 0 || discountCents <= 0 {
		return nil
	}
	db := NewPromoDao(ctx).DB
	return db.Transaction(func(tx *gorm.DB) error {
		txDao := NewPromoDaoByDB(tx)
		rec := &PromoRelease{
			OrderID:       orderID,
			RuleID:        ruleID,
			DiscountCents: discountCents,
			Reason:        reason,
		}
		created, err := txDao.CreateReleaseOnce(rec)
		if err != nil {
			return err
		}
		if !created {
			// 台账已存在：本次是重复投递，预算与事件均已落，幂等返回。
			return nil
		}
		restored, err := txDao.RestoreBudget(tx, ruleID, discountCents, budgetDay)
		if err != nil {
			return err
		}
		if restored {
			if err := tx.Model(rec).UpdateColumn("restored", true).Error; err != nil {
				return err
			}
		}
		return outbox.NewOutboxDaoByDB(tx).Insert(
			"promo", "PromoReleased", "promo.released", orderID,
			events.PromoReleased{
				OrderID:       orderID,
				RuleID:        ruleID,
				DiscountCents: discountCents,
				Reason:        reason,
				Restored:      restored,
			},
		)
	})
}

// ----------------------------------------------------------------------
// admin 后台
// ----------------------------------------------------------------------

// ErrPromoRuleNotDraft 规则不存在或已不是草稿（改 / 启用只对草稿生效）。
var ErrPromoRuleNotDraft = errors.New("规则不存在或已不是草稿")

// validateRule 建 / 改规则共用的配置校验。
func validateRule(req *PromoRuleCreateReq) error {
	if !req.EndAt.After(req.StartAt) {
		return errors.New("end_at 必须晚于 start_at")
	}
	if req.Scope == PromoScopeAll && req.ScopeRefID != 0 {
		return errors.New("全场规则的 scope_ref_id 必须为 0")
	}
	if req.Scope != PromoScopeAll && req.ScopeRefID <= 0 {
		return errors.New("类目 / 商品规则必须设置 scope_ref_id")
	}
	if req.DailyBudgetCents < 0 {
		return errors.New("daily_budget_cents 不能为负")
	}
	switch req.RuleType {
	case PromoRuleTypeAmount:
		if req.DiscountCents <= 0 {
			return errors.New("满减规则必须设置 discount_cents")
		}
		// 减额必须小于门槛：否则"满 1 分减 100 元"这类配置会让命中订单实付变 0。
		if req.DiscountCents >= req.ThresholdCents {
			return errors.New("满减金额必须小于门槛金额")
		}
	case PromoRuleTypeDiscount:
		if req.DiscountBps <= 0 || req.DiscountBps >= PromoDiscountBpsBase {
			return errors.New("折扣 bps 必须落在 (0, 10000) 内")
		}
	}
	return nil
}

// CreateRule 新建规则一律是草稿，运营核对后再调 ActivateRule 生效。
func (s *PromoSrv) CreateRule(ctx context.Context, req *PromoRuleCreateReq) (*PromoRule, error) {
	if err := validateRule(req); err != nil {
		return nil, err
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
		Status:           PromoStatusDraft,
	}
	if err := NewPromoDao(ctx).Create(r); err != nil {
		return nil, err
	}
	return r, nil
}

// UpdateRule 修改草稿。已生效的规则不能改，配错了停掉重建。
func (s *PromoSrv) UpdateRule(ctx context.Context, id uint, req *PromoRuleCreateReq) (*PromoRule, error) {
	if err := validateRule(req); err != nil {
		return nil, err
	}
	d := NewPromoDao(ctx)
	ok, err := d.Update(id, map[string]interface{}{
		"name":               req.Name,
		"rule_type":          req.RuleType,
		"scope":              req.Scope,
		"scope_ref_id":       req.ScopeRefID,
		"threshold_cents":    req.ThresholdCents,
		"discount_cents":     req.DiscountCents,
		"discount_bps":       req.DiscountBps,
		"daily_budget_cents": req.DailyBudgetCents,
		"start_at":           req.StartAt,
		"end_at":             req.EndAt,
	})
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrPromoRuleNotDraft
	}
	return d.Get(id)
}

// ActivateRule 草稿 -> 生效。
func (s *PromoSrv) ActivateRule(ctx context.Context, id uint) error {
	ok, err := NewPromoDao(ctx).Activate(id, time.Now())
	if err != nil {
		return err
	}
	if !ok {
		return ErrPromoRuleNotDraft
	}
	return nil
}

func (s *PromoSrv) ListRules(ctx context.Context) ([]*PromoRule, error) {
	return NewPromoDao(ctx).ListAll()
}

func (s *PromoSrv) StopRule(ctx context.Context, id uint) error {
	return NewPromoDao(ctx).Stop(id)
}
