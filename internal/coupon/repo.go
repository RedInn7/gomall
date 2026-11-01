package coupon

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/RedInn7/gomall/repository/cache"
	"github.com/RedInn7/gomall/repository/db/dao"
)

type CouponDao struct {
	*gorm.DB
}

func NewCouponDao(ctx context.Context) *CouponDao {
	return &CouponDao{dao.NewDBClient(ctx)}
}

func NewCouponDaoByDB(db *gorm.DB) *CouponDao {
	return &CouponDao{db}
}

func (d *CouponDao) CreateBatch(b *CouponBatch) error {
	return d.Create(b).Error
}

func (d *CouponDao) GetBatch(id uint) (*CouponBatch, error) {
	var b CouponBatch
	err := d.First(&b, id).Error
	if err != nil {
		return nil, err
	}
	return &b, nil
}

func (d *CouponDao) ListActiveBatches(now time.Time) ([]*CouponBatch, error) {
	var out []*CouponBatch
	err := d.Where("start_at <= ? AND end_at >= ?", now, now).Find(&out).Error
	return out, err
}

// ClaimWithDBLock 用 SELECT FOR UPDATE 串行化扣减
func (d *CouponDao) ClaimWithDBLock(userId, batchId uint) (*UserCoupon, error) {
	var uc *UserCoupon
	err := d.Transaction(func(tx *gorm.DB) error {
		var batch CouponBatch
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ?", batchId).First(&batch).Error; err != nil {
			return err
		}

		now := time.Now()
		if now.Before(batch.StartAt) || now.After(batch.EndAt) {
			return errors.New("活动未开始或已结束")
		}
		if batch.Claimed >= batch.Total {
			return cache.ErrCouponSoldOut
		}

		// 单用户配额
		var owned int64
		if err := tx.Model(&UserCoupon{}).
			Where("user_id = ? AND batch_id = ?", userId, batchId).
			Count(&owned).Error; err != nil {
			return err
		}
		if owned >= batch.PerUser {
			return ErrPerUserLimitReached
		}

		if err := tx.Model(&CouponBatch{}).
			Where("id = ?", batch.ID).
			Update("claimed", gorm.Expr("claimed + 1")).Error; err != nil {
			return err
		}

		uc = &UserCoupon{
			UserId:    userId,
			BatchId:   batchId,
			Code:      generateCouponCode(userId, batchId, now),
			Status:    UserCouponStatusUnused,
			ClaimedAt: now,
			ExpireAt:  now.AddDate(0, 0, batch.ValidDays),
		}
		return tx.Create(uc).Error
	})
	return uc, err
}

// PersistClaim Lua 已经把 stock 在 redis 扣减成功，这里负责落库。
// 事务内在批次行锁下二次校验总量与单用户配额，使 DB 成为发放上限的权威来源：
// 即使 Redis 计数器因重启、TTL 到期或与 db 模式混用而偏离，也不会超发。
// 返回错误时调用方负责把 Redis 侧已扣的库存补回去。
func (d *CouponDao) PersistClaim(userId, batchId uint, validDays int) (*UserCoupon, error) {
	now := time.Now()
	uc := &UserCoupon{
		UserId:    userId,
		BatchId:   batchId,
		Code:      generateCouponCode(userId, batchId, now),
		Status:    UserCouponStatusUnused,
		ClaimedAt: now,
		ExpireAt:  now.AddDate(0, 0, validDays),
	}
	err := d.Transaction(func(tx *gorm.DB) error {
		// 查批次的总量与 per_user 上限，同时加行锁防止并发绕过。
		var batch CouponBatch
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ?", batchId).First(&batch).Error; err != nil {
			return err
		}
		// 总量守卫：db 模式只加 claimed 不扣 Redis，两种模式混用时 Redis 库存会偏多。
		if batch.Claimed >= batch.Total {
			return cache.ErrCouponSoldOut
		}

		// DB 层单用户配额守卫：Redis 计数器只是前置快速路径，不能作为唯一防线。
		var owned int64
		if err := tx.Model(&UserCoupon{}).
			Where("user_id = ? AND batch_id = ?", userId, batchId).
			Count(&owned).Error; err != nil {
			return err
		}
		if owned >= batch.PerUser {
			return ErrPerUserLimitReached
		}

		if err := tx.Model(&CouponBatch{}).
			Where("id = ?", batchId).
			Update("claimed", gorm.Expr("claimed + 1")).Error; err != nil {
			return err
		}
		return tx.Create(uc).Error
	})
	if err != nil {
		return nil, err
	}
	return uc, nil
}

// ListUserCoupons 按展示状态过滤。过期任务是定时跑的，两次之间已过期但还没翻状态的券
// 按 expire_at 归到"已过期"，不出现在"未使用"里。
func (d *CouponDao) ListUserCoupons(userId uint, status int, now time.Time) ([]*UserCoupon, error) {
	var out []*UserCoupon
	q := d.Where("user_id = ?", userId).Order("id DESC")
	switch status {
	case UserCouponStatusUnused:
		q = q.Where("status = ? AND expire_at > ?", UserCouponStatusUnused, now)
	case UserCouponStatusExpired:
		q = q.Where("status = ? OR (status = ? AND expire_at <= ?)", UserCouponStatusExpired, UserCouponStatusUnused, now)
	default:
		if status > 0 {
			q = q.Where("status = ?", status)
		}
	}
	err := q.Find(&out).Error
	return out, err
}

// GetUsable 下单前查券：必须属于该用户、未使用、未过期，连同批次一起返回。
func (d *CouponDao) GetUsable(userId, userCouponId uint, now time.Time) (*UserCoupon, *CouponBatch, error) {
	var uc UserCoupon
	err := d.Where("id = ? AND user_id = ? AND status = ? AND expire_at > ?",
		userCouponId, userId, UserCouponStatusUnused, now).First(&uc).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, ErrCouponUnavailable
	}
	if err != nil {
		return nil, nil, err
	}
	b, err := d.GetBatch(uc.BatchId)
	if err != nil {
		return nil, nil, err
	}
	return &uc, b, nil
}

// UseInTx 在下单事务里核销：条件 UPDATE 只对"本人、未使用、未过期"生效，
// 同一张券被两笔并发订单同时选中时只有一笔能核销成功，另一笔整单回滚。
func (d *CouponDao) UseInTx(userCouponId, userId, orderId uint, now time.Time) error {
	res := d.DB.Model(&UserCoupon{}).
		Where("id = ? AND user_id = ? AND status = ? AND expire_at > ?",
			userCouponId, userId, UserCouponStatusUnused, now).
		Updates(map[string]interface{}{
			"status":        UserCouponStatusUsed,
			"used_order_id": orderId,
			"used_at":       now,
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrCouponUnavailable
	}
	return nil
}

// ReleaseForOrderInTx 关单 / 退款时把订单核销的券退回：还没过期回到未使用，已过期直接记为已过期。
// 条件带 used_order_id + status=used，重复调用是 no-op。
func (d *CouponDao) ReleaseForOrderInTx(userCouponId, orderId uint, now time.Time) error {
	if userCouponId == 0 {
		return nil
	}
	return d.DB.Model(&UserCoupon{}).
		Where("id = ? AND used_order_id = ? AND status = ?", userCouponId, orderId, UserCouponStatusUsed).
		Updates(map[string]interface{}{
			"status":        gorm.Expr("CASE WHEN expire_at > ? THEN ? ELSE ? END", now, UserCouponStatusUnused, UserCouponStatusExpired),
			"used_order_id": 0,
			"used_at":       nil,
		}).Error
}

// ExpireOverdue 把已过有效期的未使用券批量翻成已过期，返回翻转条数。
func (d *CouponDao) ExpireOverdue(now time.Time) (int64, error) {
	res := d.DB.Model(&UserCoupon{}).
		Where("status = ? AND expire_at <= ?", UserCouponStatusUnused, now).
		Update("status", UserCouponStatusExpired)
	return res.RowsAffected, res.Error
}

// generateCouponCode 券码 = 时间戳 + 批次 + 用户 + 随机熵。
// 时间戳只有秒级精度，同一用户同一秒内连续领取必须靠随机段避开 code 唯一索引冲突。
func generateCouponCode(userId, batchId uint, t time.Time) string {
	var entropy [4]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		binary.BigEndian.PutUint32(entropy[:], uint32(time.Now().UnixNano()))
	}
	return t.Format("20060102150405") + "-" +
		uintToStr(batchId) + "-" + uintToStr(userId) + "-" +
		hex.EncodeToString(entropy[:])
}

func uintToStr(v uint) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
