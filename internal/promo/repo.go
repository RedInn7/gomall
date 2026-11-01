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

// Create 落规则。运营创建时调用，Status 由 service 置为 draft，启用另走 Activate。
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

// BudgetDay 预算日口径：服务器时区的自然日。
func BudgetDay(t time.Time) string { return t.Format("2006-01-02") }

// AtomicConsumeBudget 在调用方提供的 tx 中扣减当日预算。
// DailyBudget = 0 视为不限，只累计不拦截。
//
// 单条 UPDATE + RowsAffected 兜底防超发，同时完成按天惰性重置：
//
//	UPDATE promo_rule
//	   SET consumed_today = CASE WHEN budget_date = 今天 THEN consumed_today + ? ELSE ? END,
//	       budget_date    = 今天
//	 WHERE id = ? AND status = active AND start_at <= now AND end_at >= now
//	   AND (daily_budget_cents = 0 OR (今天已用) + ? <= daily_budget_cents)
//
// consumed_today 必须写在 budget_date 前面：MySQL 的多列 SET 从左到右求值，后面的表达式看到的是新值。
// 影响 0 行 = 预算被并发挤光、规则已停用或已过期，返回 ErrPromoBudgetExhausted，
// 下单侧捕获后降级到次优规则或无折扣，不拒单。
func (d *PromoDao) AtomicConsumeBudget(tx *gorm.DB, ruleID uint, amount int64) error {
	if amount <= 0 {
		return nil
	}
	now := time.Now()
	today := BudgetDay(now)
	res := tx.Exec(`UPDATE promo_rule
   SET consumed_today = CASE WHEN budget_date = ? THEN consumed_today + ? ELSE ? END,
       budget_date = ?
 WHERE id = ? AND status = ? AND start_at <= ? AND end_at >= ?
   AND (daily_budget_cents = 0 OR (CASE WHEN budget_date = ? THEN consumed_today ELSE 0 END) + ? <= daily_budget_cents)`,
		today, amount, amount, today,
		ruleID, PromoStatusActive, now, now,
		today, amount)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrPromoBudgetExhausted
	}
	return nil
}

// RestoreBudget 关单 / 退款时退还预算，返回是否真的回补了。
// day 是这笔订单扣预算的那一天：只有它仍是规则当前的预算日才回补，
// 跨天的订单退回来不能挤占今天的预算（那一天的额度已经作废）。
// day 为空兼容升级前投递的旧事件，按旧口径直接回补。
// 再叠一层 consumed_today >= amount，不允许减成负数。
func (d *PromoDao) RestoreBudget(tx *gorm.DB, ruleID uint, amount int64, day string) (bool, error) {
	if amount <= 0 {
		return false, nil
	}
	q := tx.Model(&PromoRule{}).Where("id = ? AND consumed_today >= ?", ruleID, amount)
	if day != "" {
		q = q.Where("budget_date = ?", day)
	}
	res := q.UpdateColumn("consumed_today", gorm.Expr("consumed_today - ?", amount))
	return res.RowsAffected > 0, res.Error
}

// Update 只有草稿能改：已生效的规则可能已经有订单按它扣了预算，改金额会让补贴台账对不上。
func (d *PromoDao) Update(id uint, fields map[string]interface{}) (bool, error) {
	res := d.DB.Model(&PromoRule{}).Where("id = ? AND status = ?", id, PromoStatusDraft).Updates(fields)
	return res.RowsAffected > 0, res.Error
}

// Activate 草稿 -> 生效。已经过了结束时间的草稿不让启用。
func (d *PromoDao) Activate(id uint, now time.Time) (bool, error) {
	res := d.DB.Model(&PromoRule{}).
		Where("id = ? AND status = ? AND end_at > ?", id, PromoStatusDraft, now).
		Update("status", PromoStatusActive)
	return res.RowsAffected > 0, res.Error
}

// Stop 运营手动停用一条规则。Stop 后正在进行中的订单不影响，
// 新订单不再命中（ListActiveForCart 已经按 status=active 过滤）。
func (d *PromoDao) Stop(id uint) error {
	return d.DB.Model(&PromoRule{}).
		Where("id = ?", id).
		Update("status", PromoStatusStopped).Error
}
