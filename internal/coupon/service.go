package coupon

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/RedInn7/gomall/pkg/utils/ctl"
	"github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/cache"
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
func (s *CouponSrv) CreateBatch(ctx context.Context, req *CouponBatchCreateReq) (interface{}, error) {
	if err := validateBatch(req); err != nil {
		return nil, err
	}
	b := &CouponBatch{
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
	if err := NewCouponDao(ctx).CreateBatch(b); err != nil {
		return nil, err
	}
	// 把库存提前装到 Redis，过期时间设为活动结束。失败不影响建批次：
	// 领券时发现 key 不存在会按 DB 重新装载，库存对账任务也会补。
	if err := cache.InitCouponStock(ctx, b.ID, b.Total, stockTTL(b.EndAt)); err != nil {
		log.LogrusObj.Errorln("init coupon stock failed:", err)
	}
	return b, nil
}

// validateBatch 建批次的金额校验：折扣券 Amount 表示几折，必须在 (0,100)；满减券减额为正；门槛非负。
func validateBatch(req *CouponBatchCreateReq) error {
	if !req.EndAt.After(req.StartAt) {
		return errors.New("end_at 必须晚于 start_at")
	}
	if req.Threshold < 0 {
		return errors.New("threshold 不能为负")
	}
	switch req.Type {
	case CouponTypeAmount:
		if req.Amount <= 0 {
			return errors.New("满减券减额必须大于 0")
		}
		// 有门槛的券减额不能超过门槛，否则刚好满门槛的订单实付变 0（无门槛券由下单侧封顶）。
		if req.Threshold > 0 && req.Amount > req.Threshold {
			return errors.New("满减券减额不能超过门槛")
		}
	case CouponTypeDiscount:
		if req.Amount <= 0 || req.Amount >= 100 {
			return errors.New("折扣券 amount 必须在 (0, 100) 内，85 表示 85 折")
		}
	}
	return nil
}

// stockTTL Redis 库存 key 活到活动结束；已经快结束的给一天兜底。
func stockTTL(endAt time.Time) time.Duration {
	ttl := time.Until(endAt)
	if ttl < time.Minute {
		ttl = 24 * time.Hour
	}
	return ttl
}

// ListActiveBatches 列举进行中的活动
func (s *CouponSrv) ListActiveBatches(ctx context.Context) (interface{}, error) {
	return NewCouponDao(ctx).ListActiveBatches(time.Now())
}

// Claim 领券。默认走 Lua 原子扣减；mode="db" 走悲观锁，仅在服务端设置
// COUPON_ALLOW_DB_MODE=true 时生效（压测对比用），否则忽略客户端传值。
func (s *CouponSrv) Claim(ctx context.Context, mode string, batchId uint) (interface{}, error) {
	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		return nil, err
	}

	if mode == "db" && strings.EqualFold(strings.TrimSpace(os.Getenv("COUPON_ALLOW_DB_MODE")), "true") {
		return NewCouponDao(ctx).ClaimWithDBLock(u.Id, batchId)
	}

	batch, err := NewCouponDao(ctx).GetBatch(batchId)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	if now.Before(batch.StartAt) || now.After(batch.EndAt) {
		return nil, errors.New("活动未开始或已结束")
	}
	if batch.Claimed >= batch.Total {
		// DB 已发满直接拒，不再进 Redis，避免 Redis 库存偏多时压力落到批次热行上。
		return nil, cache.ErrCouponSoldOut
	}

	ok, err := cache.ClaimCouponAtomic(ctx, u.Id, batchId, batch.PerUser)
	if errors.Is(err, cache.ErrCouponStockMissing) {
		// key 丢了（预热失败 / 淘汰 / Redis 重启）：按 DB 剩余量重新装载再试一次，
		// 不能把"Redis 没数据"当成"已抢光"拒掉所有人。
		if _, rerr := cache.RaiseCouponStock(ctx, batchId, batch.Total-batch.Claimed, stockTTL(batch.EndAt)); rerr != nil {
			return nil, rerr
		}
		ok, err = cache.ClaimCouponAtomic(ctx, u.Id, batchId, batch.PerUser)
	}
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("领取失败")
	}

	uc, err := NewCouponDao(ctx).PersistClaim(u.Id, batchId, batch.ValidDays)
	if err != nil {
		// DB 已发满说明 Redis 库存偏多（对账 / 重新装载算重了在途领取），清零而不是归还。
		// 其他落库失败回滚 redis，避免库存幽灵消耗；回滚失败需告警补偿
		rollback := cache.RollbackCouponStock
		if errors.Is(err, cache.ErrCouponSoldOut) {
			rollback = cache.MarkCouponSoldOut
		}
		if rbErr := rollback(ctx, u.Id, batchId); rbErr != nil {
			log.LogrusObj.Errorf("rollback coupon stock failed, userId=%d batchId=%d: %v", u.Id, batchId, rbErr)
		}
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
	return NewCouponDao(ctx).ListUserCoupons(u.Id, status, time.Now())
}

// RunExpireCoupons 定时把过了有效期的未使用券翻成已过期。
func (s *CouponSrv) RunExpireCoupons() {
	n, err := NewCouponDao(context.Background()).ExpireOverdue(time.Now())
	if err != nil {
		log.LogrusObj.Errorf("coupon expire sweep failed: %v", err)
		return
	}
	if n > 0 {
		log.LogrusObj.Infof("coupon expire sweep: %d coupons expired", n)
	}
}

// RunStockReconcile 进行中批次的 Redis 库存对账：Redis 低于 DB 剩余量（key 丢失、落库失败后回滚也失败
// 造成的幽灵消耗）就抬回 DB 口径。DB 是发放上限的权威，只升不降，偏多的部分由落库守卫兜住。
func (s *CouponSrv) RunStockReconcile() {
	ctx := context.Background()
	batches, err := NewCouponDao(ctx).ListActiveBatches(time.Now())
	if err != nil {
		log.LogrusObj.Errorf("coupon stock reconcile list batches failed: %v", err)
		return
	}
	for _, b := range batches {
		raised, err := cache.RaiseCouponStock(ctx, b.ID, b.Total-b.Claimed, stockTTL(b.EndAt))
		if err != nil {
			log.LogrusObj.Errorf("coupon stock reconcile batch=%d failed: %v", b.ID, err)
			continue
		}
		if raised {
			log.LogrusObj.Warnf("coupon stock reconcile batch=%d raised to %d", b.ID, b.Total-b.Claimed)
		}
	}
}
