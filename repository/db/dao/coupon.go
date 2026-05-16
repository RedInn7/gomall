package dao

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"github.com/RedInn7/gomall/repository/db/model"
)

type CouponDao struct {
	*gorm.DB
}

func NewCouponDao(ctx context.Context) *CouponDao {
	return &CouponDao{NewDBClient(ctx)}
}

func (d *CouponDao) CreateBatch(b *model.CouponBatch) error {
	return d.Create(b).Error
}

func (d *CouponDao) GetBatch(id uint) (*model.CouponBatch, error) {
	var b model.CouponBatch
	err := d.First(&b, id).Error
	if err != nil {
		return nil, err
	}
	return &b, nil
}

func (d *CouponDao) ListActiveBatches(now time.Time) ([]*model.CouponBatch, error) {
	var out []*model.CouponBatch
	err := d.Where("start_at <= ? AND end_at >= ?", now, now).Find(&out).Error
	return out, err
}

// ClaimWithDBLock 用 SELECT FOR UPDATE 串行化扣减
func (d *CouponDao) ClaimWithDBLock(userId, batchId uint) (*model.UserCoupon, error) {
	var uc *model.UserCoupon
	err := d.Transaction(func(tx *gorm.DB) error {
		var batch model.CouponBatch
		if err := tx.Clauses().Set("gorm:query_option", "FOR UPDATE").
			Where("id = ?", batchId).First(&batch).Error; err != nil {
			return err
		}

		now := time.Now()
		if now.Before(batch.StartAt) || now.After(batch.EndAt) {
			return errors.New("活动未开始或已结束")
		}
		if batch.Claimed >= batch.Total {
			return errors.New("已抢光")
		}

		// 单用户配额
		var owned int64
		if err := tx.Model(&model.UserCoupon{}).
			Where("user_id = ? AND batch_id = ?", userId, batchId).
			Count(&owned).Error; err != nil {
			return err
		}
		if owned >= batch.PerUser {
			return errors.New("超出单人领取上限")
		}

		if err := tx.Model(&model.CouponBatch{}).
			Where("id = ?", batch.ID).
			Update("claimed", gorm.Expr("claimed + 1")).Error; err != nil {
			return err
		}

		uc = &model.UserCoupon{
			UserId:    userId,
			BatchId:   batchId,
			Code:      generateCouponCode(userId, batchId, now),
			Status:    model.UserCouponStatusUnused,
			ClaimedAt: now,
			ExpireAt:  now.AddDate(0, 0, batch.ValidDays),
		}
		return tx.Create(uc).Error
	})
	return uc, err
}

// PersistClaim Lua 已经把 stock 在 redis 扣减成功，这里只负责落库（不再校验总量）
func (d *CouponDao) PersistClaim(userId, batchId uint, validDays int) (*model.UserCoupon, error) {
	now := time.Now()
	uc := &model.UserCoupon{
		UserId:    userId,
		BatchId:   batchId,
		Code:      generateCouponCode(userId, batchId, now),
		Status:    model.UserCouponStatusUnused,
		ClaimedAt: now,
		ExpireAt:  now.AddDate(0, 0, validDays),
	}
	err := d.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.CouponBatch{}).
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

func (d *CouponDao) ListUserCoupons(userId uint, status int) ([]*model.UserCoupon, error) {
	var out []*model.UserCoupon
	q := d.Where("user_id = ?", userId).Order("id DESC")
	if status > 0 {
		q = q.Where("status = ?", status)
	}
	err := q.Find(&out).Error
	return out, err
}

func generateCouponCode(userId, batchId uint, t time.Time) string {
	return t.Format("20060102150405") + "-" +
		uintToStr(batchId) + "-" + uintToStr(userId)
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
