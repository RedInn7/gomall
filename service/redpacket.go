package service

import (
	"context"
	"errors"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/RedInn7/gomall/pkg/utils/ctl"
	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/cache"
	"github.com/RedInn7/gomall/repository/db/dao"
	"github.com/RedInn7/gomall/repository/db/model"
	"github.com/RedInn7/gomall/service/events"
	"github.com/RedInn7/gomall/types"
)

const (
	defaultRedPacketTTL = 24 * time.Hour
	maxRedPacketTTL     = 7 * 24 * time.Hour
)

var (
	redPacketSrvIns  *RedPacketSrv
	redPacketSrvOnce sync.Once
)

type RedPacketSrv struct{}

func GetRedPacketSrv() *RedPacketSrv {
	redPacketSrvOnce.Do(func() { redPacketSrvIns = &RedPacketSrv{} })
	return redPacketSrvIns
}

// Create 发红包：
//  1. 二倍均值法预拆好 count 份金额数组
//  2. DB 事务：写 red_packet 主记录 + outbox(red_packet.created)
//  3. Redis Lua 一把 RPUSH 金额数组 + EXPIRE
//     钱包扣账由下游消费 red_packet.created 事件入账
func (s *RedPacketSrv) Create(ctx context.Context, req *types.RedPacketCreateReq) (interface{}, error) {
	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		return nil, err
	}
	if req.Total < int64(req.Count) {
		return nil, errors.New("总额必须 ≥ 份数 (每份至少 1 分)")
	}

	ttl := time.Duration(req.ExpireSec) * time.Second
	if ttl <= 0 {
		ttl = defaultRedPacketTTL
	}
	if ttl > maxRedPacketTTL {
		ttl = maxRedPacketTTL
	}
	expireAt := time.Now().Add(ttl)

	amounts, err := cache.SplitRedPacket(req.Total, req.Count)
	if err != nil {
		return nil, err
	}

	rp := &model.RedPacket{
		UserID:    u.Id,
		Total:     req.Total,
		Count:     req.Count,
		Remaining: req.Count,
		ExpireAt:  expireAt,
		Status:    model.RedPacketStatusActive,
		Greeting:  req.Greeting,
	}

	err = dao.NewDBClient(ctx).Transaction(func(tx *gorm.DB) error {
		if e := dao.NewRedPacketDaoByDB(tx).Create(rp); e != nil {
			return e
		}
		return dao.NewOutboxDaoByDB(tx).Insert(
			"red_packet", "RedPacketCreated", "red_packet.created", rp.ID,
			events.RedPacketCreated{
				RedPacketID: rp.ID,
				UserID:      u.Id,
				Total:       req.Total,
				Count:       req.Count,
				Greeting:    req.Greeting,
				ExpireAt:    expireAt.Unix(),
			},
		)
	})
	if err != nil {
		util.LogrusObj.Errorf("redpacket create tx failed: %v", err)
		return nil, err
	}

	// Redis list TTL 比红包过期多留 1h，给 cron 兜底回收留窗口
	if e := cache.PrepareRedPacket(ctx, rp.ID, amounts, ttl+time.Hour); e != nil {
		util.LogrusObj.Errorf("prepare redpacket cache failed id=%d err=%v", rp.ID, e)
		return nil, e
	}

	return toRedPacketResp(rp), nil
}

// Claim 抢红包：
//  1. Lua 原子 LPOP + 标记 claimed -> amount
//  2. DB 事务：写 RedPacketClaim + remaining-- + outbox(red_packet.claimed)
//  3. DB 事务失败 -> Saga 回滚 Lua (LPUSH 金额回 list, 撤销 claimed 标记)
func (s *RedPacketSrv) Claim(ctx context.Context, req *types.RedPacketClaimReq) (interface{}, error) {
	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		return nil, err
	}

	rpDao := dao.NewRedPacketDao(ctx)
	rp, err := rpDao.Get(req.ID)
	if err != nil {
		return nil, errors.New("红包不存在")
	}
	if rp.Status != model.RedPacketStatusActive {
		return nil, errors.New("红包已结束")
	}
	if time.Now().After(rp.ExpireAt) {
		return nil, errors.New("红包已过期")
	}

	claimedTTL := time.Until(rp.ExpireAt) + 24*time.Hour
	amount, err := cache.ClaimRedPacket(ctx, rp.ID, u.Id, claimedTTL)
	if err != nil {
		return nil, err
	}

	txErr := rpDao.DB.Transaction(func(tx *gorm.DB) error {
		txDao := dao.NewRedPacketDaoByDB(tx)
		claim := &model.RedPacketClaim{
			RedPacketID: rp.ID,
			UserID:      u.Id,
			Amount:      amount,
		}
		if e := txDao.CreateClaim(claim); e != nil {
			return e
		}
		if _, e := txDao.DecrRemaining(rp.ID); e != nil {
			return e
		}
		return dao.NewOutboxDaoByDB(tx).Insert(
			"red_packet", "RedPacketClaimed", "red_packet.claimed", rp.ID,
			events.RedPacketClaimed{
				RedPacketID: rp.ID,
				UserID:      u.Id,
				Amount:      amount,
			},
		)
	})
	if txErr != nil {
		util.LogrusObj.Errorf("redpacket claim tx failed id=%d uid=%d err=%v",
			rp.ID, u.Id, txErr)
		if rbErr := cache.RollbackRedPacketClaim(ctx, rp.ID, u.Id, amount); rbErr != nil {
			util.LogrusObj.Errorf("redpacket claim rollback failed id=%d uid=%d err=%v",
				rp.ID, u.Id, rbErr)
		}
		return nil, txErr
	}

	// 抢完后异步置位 finished (有竞争也只是状态稍滞后，cron 兜底)
	if remain, _ := cache.GetRedPacketRemainingCount(ctx, rp.ID); remain == 0 {
		if e := rpDao.MarkStatus(rp.ID, model.RedPacketStatusFinished); e != nil {
			util.LogrusObj.Errorf("mark redpacket finished failed id=%d err=%v", rp.ID, e)
		}
	}

	return &types.RedPacketClaimResp{
		RedPacketID: rp.ID,
		Amount:      amount,
	}, nil
}

// Show 红包详情 + 领取明细
func (s *RedPacketSrv) Show(ctx context.Context, req *types.RedPacketShowReq) (interface{}, error) {
	rpDao := dao.NewRedPacketDao(ctx)
	rp, err := rpDao.Get(req.ID)
	if err != nil {
		return nil, errors.New("红包不存在")
	}
	claims, err := rpDao.ListClaims(rp.ID)
	if err != nil {
		return nil, err
	}
	items := make([]*types.RedPacketClaimItem, 0, len(claims))
	for _, c := range claims {
		items = append(items, &types.RedPacketClaimItem{UserID: c.UserID, Amount: c.Amount})
	}
	return &types.RedPacketDetailResp{
		RedPacket: toRedPacketResp(rp),
		Claims:    items,
	}, nil
}

// ListMine 我发出过的红包
func (s *RedPacketSrv) ListMine(ctx context.Context, req *types.RedPacketListReq) (interface{}, error) {
	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := dao.NewRedPacketDao(ctx).ListMine(u.Id, req.LastID, req.PageSize)
	if err != nil {
		return nil, err
	}
	resp := &types.RedPacketListResp{List: make([]*types.RedPacketResp, 0, len(rows))}
	for _, r := range rows {
		resp.List = append(resp.List, toRedPacketResp(r))
	}
	if len(rows) > 0 {
		resp.LastID = rows[len(rows)-1].ID
	}
	return resp, nil
}

func toRedPacketResp(rp *model.RedPacket) *types.RedPacketResp {
	return &types.RedPacketResp{
		ID:        rp.ID,
		UserID:    rp.UserID,
		Total:     rp.Total,
		Count:     rp.Count,
		Remaining: rp.Remaining,
		ExpireAt:  rp.ExpireAt.Unix(),
		Status:    rp.Status,
		Greeting:  rp.Greeting,
	}
}
