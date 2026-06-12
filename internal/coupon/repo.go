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

	"github.com/RedInn7/gomall/repository/db/dao"
)

type CouponDao struct {
	*gorm.DB
}

func NewCouponDao(ctx context.Context) *CouponDao {
	return &CouponDao{dao.NewDBClient(ctx)}
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
			return errors.New("已抢光")
		}

		// 单用户配额
		var owned int64
		if err := tx.Model(&UserCoupon{}).
			Where("user_id = ? AND batch_id = ?", userId, batchId).
			Count(&owned).Error; err != nil {
			return err
		}
		if owned >= batch.PerUser {
			return errors.New("超出单人领取上限")
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

// PersistClaim Lua 已经把 stock 在 redis 扣减成功，这里只负责落库（不再校验总量）
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

func (d *CouponDao) ListUserCoupons(userId uint, status int) ([]*UserCoupon, error) {
	var out []*UserCoupon
	q := d.Where("user_id = ?", userId).Order("id DESC")
	if status > 0 {
		q = q.Where("status = ?", status)
	}
	err := q.Find(&out).Error
	return out, err
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
