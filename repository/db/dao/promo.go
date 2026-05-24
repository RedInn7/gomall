package dao

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"github.com/RedInn7/gomall/repository/db/model"
)

// ErrPromoBudgetExhausted 当日预算用尽。业务层捕获后转 80003 业务码返回前端 / 客服。
var ErrPromoBudgetExhausted = errors.New("promo daily budget exhausted")

type PromoDao struct {
	*gorm.DB
}

func NewPromoDao(ctx context.Context) *PromoDao {
	return &PromoDao{NewDBClient(ctx)}
}

func NewPromoDaoByDB(db *gorm.DB) *PromoDao {
	return &PromoDao{db}
}

// Create 落规则。运营创建时调用，默认 Status=draft。
func (d *PromoDao) Create(r *model.PromoRule) error {
	return d.DB.Create(r).Error
}

// Get 单条
func (d *PromoDao) Get(id uint) (*model.PromoRule, error) {
	var r model.PromoRule
	if err := d.DB.First(&r, id).Error; err != nil {
		return nil, err
	}
	return &r, nil
}

// ListAll 后台列规则，按 id 倒序
func (d *PromoDao) ListAll() ([]*model.PromoRule, error) {
	var rows []*model.PromoRule
	err := d.DB.Order("id DESC").Find(&rows).Error
	return rows, err
}

// ListActiveRules 取当前生效、范围匹配的规则集合。
//
//	scope=PromoScopeAll：忽略 scopeRefIDs，仅拉全场规则；
//	scope=Category / Product：同时拉全场 + 该 scope 下 ref id 在 scopeRefIDs 内的；
//
// 引擎在 service 层把购物车里出现过的 category_id / product_id 组装好后一次性传进来，
// 避免在 DAO 多次查 DB。
func (d *PromoDao) ListActiveRules(now time.Time, scope int, scopeRefIDs []int64) ([]*model.PromoRule, error) {
	q := d.DB.Where("status = ? AND start_at <= ? AND end_at >= ?",
		model.PromoStatusActive, now, now)

	switch scope {
	case model.PromoScopeAll:
		q = q.Where("scope = ?", model.PromoScopeAll)
	default:
		if len(scopeRefIDs) == 0 {
			q = q.Where("scope = ?", model.PromoScopeAll)
		} else {
			q = q.Where("scope = ? OR (scope = ? AND scope_ref_id IN ?)",
				model.PromoScopeAll, scope, scopeRefIDs)
		}
	}

	var rows []*model.PromoRule
	err := q.Order("id ASC").Find(&rows).Error
	return rows, err
}

// ListActiveForCart 同时考虑类目和商品两种 scope。供引擎一次拉齐。
func (d *PromoDao) ListActiveForCart(now time.Time, categoryIDs, productIDs []int64) ([]*model.PromoRule, error) {
	q := d.DB.Where("status = ? AND start_at <= ? AND end_at >= ?",
		model.PromoStatusActive, now, now)

	// 组装 OR：全场 OR 命中类目 OR 命中商品
	conds := d.DB.Where("scope = ?", model.PromoScopeAll)
	if len(categoryIDs) > 0 {
		conds = conds.Or("scope = ? AND scope_ref_id IN ?", model.PromoScopeCategory, categoryIDs)
	}
	if len(productIDs) > 0 {
		conds = conds.Or("scope = ? AND scope_ref_id IN ?", model.PromoScopeProduct, productIDs)
	}
	q = q.Where(conds)

	var rows []*model.PromoRule
	err := q.Order("id ASC").Find(&rows).Error
	return rows, err
}

// AtomicConsumeBudget 在调用方提供的 tx 中扣减预算。
// DailyBudget = 0 视为不限，直接返回 nil。
//
// 用单条 UPDATE + RowsAffected 兜底防超发：
//
//	UPDATE promo_rule
//	   SET consumed_today = consumed_today + ?
//	 WHERE id = ?
//	   AND status = active
//	   AND (daily_budget_cents = 0 OR consumed_today + ? <= daily_budget_cents)
//
// 影响 0 行 = 预算被同事务并发挤光，返回 ErrPromoBudgetExhausted，
// 业务层捕获后回滚整笔订单的 promo 应用并落 80003。
func (d *PromoDao) AtomicConsumeBudget(tx *gorm.DB, ruleID uint, amount int64) error {
	if amount <= 0 {
		return nil
	}
	res := tx.Model(&model.PromoRule{}).
		Where("id = ? AND status = ? AND (daily_budget_cents = 0 OR consumed_today + ? <= daily_budget_cents)",
			ruleID, model.PromoStatusActive, amount).
		UpdateColumn("consumed_today", gorm.Expr("consumed_today + ?", amount))
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrPromoBudgetExhausted
	}
	return nil
}

// RestoreBudget 关单 / 退款时退还预算。
// 不允许减成负数，再叠一层 WHERE consumed_today >= ? 兜底。
// 即便 ConsumedToday 已经因为 cron 重置归零，也只是 RowsAffected=0，不返回错误。
func (d *PromoDao) RestoreBudget(tx *gorm.DB, ruleID uint, amount int64) error {
	if amount <= 0 {
		return nil
	}
	return tx.Model(&model.PromoRule{}).
		Where("id = ? AND consumed_today >= ?", ruleID, amount).
		UpdateColumn("consumed_today", gorm.Expr("consumed_today - ?", amount)).Error
}

// Stop 运营手动停用一条规则。Stop 后正在进行中的订单不影响，
// 新订单不再命中（ListActiveRules 已经按 status=active 过滤）。
func (d *PromoDao) Stop(id uint) error {
	return d.DB.Model(&model.PromoRule{}).
		Where("id = ?", id).
		Update("status", model.PromoStatusStopped).Error
}
