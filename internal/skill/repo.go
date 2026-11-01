package skill

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/internal/order"
	"github.com/RedInn7/gomall/pkg/e"
	"github.com/RedInn7/gomall/repository/db/dao"
)

type SkillGoodsDao struct {
	*gorm.DB
}

func NewSkillGoodsDao(ctx context.Context) *SkillGoodsDao {
	return &SkillGoodsDao{dao.NewDBClient(ctx)}
}

func NewSkillGoodsDaoByDB(db *gorm.DB) *SkillGoodsDao {
	return &SkillGoodsDao{db}
}

func (d *SkillGoodsDao) Create(in *SkillProduct) error {
	return d.Model(&SkillProduct{}).Create(in).Error
}

// GetByProductStart 按幂等键 (product_id, start_at) 查活动，不存在返回 nil。
func (d *SkillGoodsDao) GetByProductStart(productId uint, startAt time.Time) (*SkillProduct, error) {
	var sp SkillProduct
	err := d.Where("product_id = ? AND start_at = ?", productId, startAt).First(&sp).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &sp, nil
}

// GetById 查活动，不存在返回 nil。
func (d *SkillGoodsDao) GetById(id uint) (*SkillProduct, error) {
	var sp SkillProduct
	err := d.Where("id = ?", id).First(&sp).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &sp, nil
}

// ListNotEnded 未结束（进行中 + 未开始）的活动，按开始时间排序。
func (d *SkillGoodsDao) ListNotEnded(now time.Time) (resp []*SkillProduct, err error) {
	err = d.Where("end_at > ?", now).Order("start_at ASC, id ASC").Find(&resp).Error
	return
}

// ListForReconcile 秒杀对账范围：Redis key 还活着的活动（结束不到 skillKeyGrace）。
func (d *SkillGoodsDao) ListForReconcile(now time.Time) (resp []*SkillProduct, err error) {
	err = d.Where("end_at > ?", now.Add(-skillKeyGrace)).Find(&resp).Error
	return
}

// ListUsers 某场活动所有仍持有名额的用户：userID -> 件数。
func (d *SkillGoodsDao) ListUsers(skillId uint) (map[uint]int64, error) {
	var rows []SkillUser
	if err := d.Where("skill_product_id = ? AND bought > 0", skillId).Find(&rows).Error; err != nil {
		return nil, err
	}
	m := make(map[uint]int64, len(rows))
	for _, r := range rows {
		m[r.UserId] = int64(r.Bought)
	}
	return m, nil
}

// Occupy 建单同事务的 DB 兜底：条件扣活动库存（sold + n <= num），再给用户成单数 +n（不超限购）。
// Redis 漂移放进来的多余请求在这里被挡下，事务回滚。
// 用户行先「不存在才插入 bought=0」再条件 UPDATE：同一用户的首批单并发落库也不会撞唯一索引被误拒；
// 前面扣活动 sold 拿了活动行的行锁，同一活动的建单事务在这里已经串行。
func (d *SkillGoodsDao) Occupy(sp *SkillProduct, userId uint, n int) error {
	res := d.Model(&SkillProduct{}).
		Where("id = ? AND sold + ? <= num", sp.Id, n).
		UpdateColumn("sold", gorm.Expr("sold + ?", n))
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return e.New(e.ErrSkillSoldOut)
	}
	if err := d.DB.Clauses(clause.OnConflict{DoNothing: true}).
		Create(&SkillUser{SkillProductId: sp.Id, UserId: userId}).Error; err != nil {
		return err
	}
	res = d.Model(&SkillUser{}).
		Where("skill_product_id = ? AND user_id = ? AND bought + ? <= ?", sp.Id, userId, n, sp.PerUserLimit).
		UpdateColumn("bought", gorm.Expr("bought + ?", n))
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return e.New(e.ErrSkillLimitExceeded)
	}
	return nil
}

// dbView 一场活动在 DB 里的一致快照（同一事务内读）。
type dbView struct {
	sp      *SkillProduct
	bought  map[uint]int64  // 用户有效成单数
	settled map[string]bool // 给定 ticket 里已经建出订单的
	closed  []order.Order   // 已关单的订单（id, user_id）
}

// loadDBView 在一个事务里读活动、用户成单数、给定 ticket 是否已建单、已关单订单，供预热 / 对账使用。
func loadDBView(ctx context.Context, skillId uint, tickets []string) (*dbView, error) {
	v := &dbView{settled: make(map[string]bool)}
	err := dao.NewDBClient(ctx).Transaction(func(tx *gorm.DB) error {
		d := NewSkillGoodsDaoByDB(tx)
		sp, err := d.GetById(skillId)
		if err != nil {
			return err
		}
		if sp == nil {
			return e.New(e.ErrSkillNotExist)
		}
		v.sp = sp
		if v.bought, err = d.ListUsers(skillId); err != nil {
			return err
		}
		for i := 0; i < len(tickets); i += 500 {
			var got []string
			if err := tx.Model(&order.Order{}).Where("ticket IN ?", tickets[i:min(i+500, len(tickets))]).
				Pluck("ticket", &got).Error; err != nil {
				return err
			}
			for _, t := range got {
				v.settled[t] = true
			}
		}
		return tx.Model(&order.Order{}).Select("id, user_id").
			Where("seckill_id = ? AND type = ?", skillId, consts.OrderClosed).Find(&v.closed).Error
	})
	return v, err
}

// Release 关单同事务归还活动库存与用户成单数，条件更新不会减成负数。
func (d *SkillGoodsDao) Release(skillId, userId uint, n int) error {
	if err := d.Model(&SkillProduct{}).
		Where("id = ? AND sold >= ?", skillId, n).
		UpdateColumn("sold", gorm.Expr("sold - ?", n)).Error; err != nil {
		return err
	}
	return d.Model(&SkillUser{}).
		Where("skill_product_id = ? AND user_id = ? AND bought >= ?", skillId, userId, n).
		UpdateColumn("bought", gorm.Expr("bought - ?", n)).Error
}
