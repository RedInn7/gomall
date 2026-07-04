package promo

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/RedInn7/gomall/repository/db/dao"
)

// ErrPromoBudgetExhausted 当日预算用尽。业务层捕获后转 80003 业务码返回前端 / 客服。
var ErrPromoBudgetExhausted = errors.New("promo daily budget exhausted")

type PromoDao struct {
	*gorm.DB
}

func NewPromoDao(ctx context.Context) *PromoDao {
	return &PromoDao{dao.NewDBClient(ctx)}
}

func NewPromoDaoByDB(db *gorm.DB) *PromoDao {
	return &PromoDao{db}
}

// CreateReleaseOnce 幂等落 promo_release 台账（order_id 唯一索引 + ON CONFLICT DO NOTHING）。
// created=false 表示台账已存在，即同一订单的重复投递。
func (d *PromoDao) CreateReleaseOnce(rec *PromoRelease) (created bool, err error) {
	res := d.DB.Clauses(clause.OnConflict{DoNothing: true}).Create(rec)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// Create 落规则。运营创建时调用，默认 Status=draft。
func (d *PromoDao) Create(r *PromoRule) error {
	return d.DB.Create(r).Error
}

// Get 单条
func (d *PromoDao) Get(id uint) (*PromoRule, error) {
	var r PromoRule
	if err := d.DB.First(&r, id).Error; err != nil {
		return nil, err
	}
	return &r, nil
}

// ListAll 后台列规则，按 id 倒序
func (d *PromoDao) ListAll() ([]*PromoRule, error) {
	var rows []*PromoRule
	err := d.DB.Order("id DESC").Find(&rows).Error
	return rows, err
}

// ListActiveForCart 同时考虑类目和商品两种 scope。供引擎一次拉齐。
func (d *PromoDao) ListActiveForCart(now time.Time, categoryIDs, productIDs []int64) ([]*PromoRule, error) {
	q := d.DB.Where("status = ? AND start_at <= ? AND end_at >= ?",
		PromoStatusActive, now, now)

	// 组装 OR：全场 OR 命中类目 OR 命中商品
	conds := d.DB.Where("scope = ?", PromoScopeAll)
	if len(categoryIDs) > 0 {
		conds = conds.Or("scope = ? AND scope_ref_id IN ?", PromoScopeCategory, categoryIDs)
	}
	if len(productIDs) > 0 {
		conds = conds.Or("scope = ? AND scope_ref_id IN ?", PromoScopeProduct, productIDs)
	}
	q = q.Where(conds)

	var rows []*PromoRule
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
	res := tx.Model(&PromoRule{}).
		Where("id = ? AND status = ? AND (daily_budget_cents = 0 OR consumed_today + ? <= daily_budget_cents)",
			ruleID, PromoStatusActive, amount).
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
	return tx.Model(&PromoRule{}).
		Where("id = ? AND consumed_today >= ?", ruleID, amount).
		UpdateColumn("consumed_today", gorm.Expr("consumed_today - ?", amount)).Error
}

// Stop 运营手动停用一条规则。Stop 后正在进行中的订单不影响，
// 新订单不再命中（ListActiveForCart 已经按 status=active 过滤）。
func (d *PromoDao) Stop(id uint) error {
	return d.DB.Model(&PromoRule{}).
		Where("id = ?", id).
		Update("status", PromoStatusStopped).Error
}
