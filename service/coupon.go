package service

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/RedInn7/gomall/pkg/utils/ctl"
	"github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/cache"
	"github.com/RedInn7/gomall/repository/db/dao"
	"github.com/RedInn7/gomall/repository/db/model"
	"github.com/RedInn7/gomall/types"
)

var (
	couponSrvIns  *CouponSrv
	couponSrvOnce sync.Once
)

type CouponSrv struct{}

func GetCouponSrv() *CouponSrv {
	couponSrvOnce.Do(func() { couponSrvIns = &CouponSrv{} })
	return couponSrvIns
}

// CreateBatch 创建批次：DB 落 + Redis 库存预热
func (s *CouponSrv) CreateBatch(ctx context.Context, req *types.CouponBatchCreateReq) (interface{}, error) {
	if !req.EndAt.After(req.StartAt) {
		return nil, errors.New("end_at 必须晚于 start_at")
	}
	b := &model.CouponBatch{
		Name:      req.Name,
		Type:      req.Type,
		Threshold: req.Threshold,
		Amount:    req.Amount,
		Total:     req.Total,
		PerUser:   req.PerUser,
		StartAt:   req.StartAt,
		EndAt:     req.EndAt,
		ValidDays: req.ValidDays,
	}
	if err := dao.NewCouponDao(ctx).CreateBatch(b); err != nil {
		return nil, err
	}
	// 把库存提前装到 Redis，过期时间设为活动结束
	ttl := time.Until(req.EndAt)
	if ttl < time.Minute {
		ttl = 24 * time.Hour
	}
	if err := cache.InitCouponStock(ctx, b.ID, b.Total, ttl); err != nil {
		log.LogrusObj.Errorln("init coupon stock failed:", err)
	}
	return b, nil
}

// ListActiveBatches 列举进行中的活动
func (s *CouponSrv) ListActiveBatches(ctx context.Context) (interface{}, error) {
	return dao.NewCouponDao(ctx).ListActiveBatches(time.Now())
}

// Claim 领券。mode="db" 走悲观锁；其余走 Lua 原子扣减
func (s *CouponSrv) Claim(ctx context.Context, mode string, batchId uint) (interface{}, error) {
	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		return nil, err
	}

	if mode == "db" {
		return dao.NewCouponDao(ctx).ClaimWithDBLock(u.Id, batchId)
	}

	batch, err := dao.NewCouponDao(ctx).GetBatch(batchId)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	if now.Before(batch.StartAt) || now.After(batch.EndAt) {
		return nil, errors.New("活动未开始或已结束")
	}

	ok, err := cache.ClaimCouponAtomic(ctx, u.Id, batchId, batch.PerUser)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("领取失败")
	}

	uc, err := dao.NewCouponDao(ctx).PersistClaim(u.Id, batchId, batch.ValidDays)
	if err != nil {
		// 落库失败回滚 redis，避免库存幽灵消耗
		cache.RollbackCouponStock(ctx, u.Id, batchId)
		return nil, err
	}
	return uc, nil
}

// ListMyCoupons 我的优惠券
func (s *CouponSrv) ListMyCoupons(ctx context.Context, status int) (interface{}, error) {
	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		return nil, err
	}
	return dao.NewCouponDao(ctx).ListUserCoupons(u.Id, status)
}
