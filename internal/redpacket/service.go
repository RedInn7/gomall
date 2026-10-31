package redpacket

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/RedInn7/gomall/internal/shared/outbox"
	"github.com/RedInn7/gomall/internal/user"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/cache"
	"github.com/RedInn7/gomall/repository/db/dao"
	"github.com/RedInn7/gomall/service/events"
)

const (
	defaultRedPacketTTL = 24 * time.Hour
	maxRedPacketTTL     = 7 * 24 * time.Hour
)

// errRedPacketClosed 领取事务里发现红包已不在进行中（已被过期回收）。
var errRedPacketClosed = errors.New("红包已结束")

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
//  2. DB 事务：写 red_packet 主记录 + 发包人钱包扣 total 进平台清算 escrow(同事务写台账) + outbox(red_packet.created)
//  3. Redis Lua 一把 RPUSH 金额数组 + EXPIRE
//
// 资金在第 2 步同事务原子落地（settleSendTx）：发包人 debit / 清算 credit，幂等由台账唯一索引兜底。
// red_packet.created 事件仅用于风控 / 数据同步，不再承担入账。
func (s *RedPacketSrv) Create(ctx context.Context, req *RedPacketCreateReq) (interface{}, error) {
	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		return nil, err
	}
	if req.Total < int64(req.Count) {
		return nil, errors.New("总额必须 ≥ 份数 (每份至少 1 分)")
	}
	if req.Total > MaxRedPacketTotalCents {
		return nil, fmt.Errorf("红包总额不能超过 %d 分", MaxRedPacketTotalCents)
	}
	// 发红包是从钱包出账，和 paydown 一样先校验支付密码。
	sender, err := user.NewUserDao(ctx).GetUserById(u.Id)
	if err != nil {
		return nil, err
	}
	if err = user.VerifyMoneyPassword(ctx, sender, req.Key); err != nil {
		return nil, err
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

	rp := &RedPacket{
		UserID:    u.Id,
		Total:     req.Total,
		Count:     req.Count,
		Remaining: req.Count,
		ExpireAt:  expireAt,
		Status:    RedPacketStatusActive,
		Greeting:  req.Greeting,
	}

	err = dao.NewDBClient(ctx).Transaction(func(tx *gorm.DB) error {
		if e := NewRedPacketDaoByDB(tx).Create(rp); e != nil {
			return e
		}
		// 发包人钱包扣 total 进平台清算 escrow，与红包主记录同事务原子落地。
		if e := settleSendTx(tx, rp.ID, u.Id, req.Total); e != nil {
			return e
		}
		return outbox.NewOutboxDaoByDB(tx).Insert(
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
		// 钱已扣进 escrow 但红包抢不了：立即按 DB 把总额退回发包人。补偿也失败时，
		// 红包过期后回收任务按 DB 剩余金额兜底退款。
		if _, rbErr := refundRedPacketLeft(ctx, rp.ID); rbErr != nil {
			util.LogrusObj.Errorf("refund unprepared redpacket failed id=%d err=%v", rp.ID, rbErr)
		}
		return nil, e
	}

	return toRedPacketResp(rp), nil
}

// Claim 抢红包：
//  1. Lua 原子 LPOP + 标记 claimed -> amount
//  2. DB 事务：写 RedPacketClaim + remaining--（DB 剩余份额为准，归零即置 finished）+ 领取人钱包入账 amount
//     (同事务写台账，escrow 出账) + outbox(red_packet.claimed)
//  3. DB 事务失败 -> Saga 回滚 Lua (LPUSH 金额回 list, 撤销 claimed 标记)；红包已被回收则不回滚
//
// 资金在第 2 步同事务原子落地（settleClaimTx）：领取人 credit / 清算 debit，
// ref 用领取记录 id 保证同红包不同领取人各自唯一、只入账一次。
func (s *RedPacketSrv) Claim(ctx context.Context, req *RedPacketClaimReq) (interface{}, error) {
	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		return nil, err
	}

	rpDao := NewRedPacketDao(ctx)
	rp, err := rpDao.Get(req.ID)
	if err != nil {
		return nil, errors.New("红包不存在")
	}
	if rp.Status != RedPacketStatusActive {
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
		txDao := NewRedPacketDaoByDB(tx)
		claim := &RedPacketClaim{
			RedPacketID: rp.ID,
			UserID:      u.Id,
			Amount:      amount,
		}
		if e := txDao.CreateClaim(claim); e != nil {
			return e
		}
		// DB 是剩余份额的权威：红包已被过期回收（剩余金额已退回发包人）时这里影响 0 行，整笔领取作废。
		n, e := txDao.DecrRemaining(rp.ID)
		if e != nil {
			return e
		}
		if n == 0 {
			return errRedPacketClosed
		}
		if e := txDao.MarkFinishedIfDrained(rp.ID); e != nil {
			return e
		}
		// 领取人钱包入账，escrow 出账；ref=领取记录 id（CreateClaim 后已回填自增主键）。
		if e := settleClaimTx(tx, claim.ID, u.Id, amount); e != nil {
			return e
		}
		return outbox.NewOutboxDaoByDB(tx).Insert(
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
		// 红包已结束时剩余金额已按 DB 退回，这份不能再塞回 Redis 给别人抢。
		if !errors.Is(txErr, errRedPacketClosed) {
			if rbErr := cache.RollbackRedPacketClaim(ctx, rp.ID, u.Id, amount); rbErr != nil {
				util.LogrusObj.Errorf("redpacket claim rollback failed id=%d uid=%d err=%v",
					rp.ID, u.Id, rbErr)
			}
		}
		return nil, txErr
	}

	return &RedPacketClaimResp{
		RedPacketID: rp.ID,
		Amount:      amount,
	}, nil
}

// Show 红包详情 + 领取明细
func (s *RedPacketSrv) Show(ctx context.Context, req *RedPacketShowReq) (interface{}, error) {
	rpDao := NewRedPacketDao(ctx)
	rp, err := rpDao.Get(req.ID)
	if err != nil {
		return nil, errors.New("红包不存在")
	}
	claims, err := rpDao.ListClaims(rp.ID)
	if err != nil {
		return nil, err
	}
	items := make([]*RedPacketClaimItem, 0, len(claims))
	for _, c := range claims {
		items = append(items, &RedPacketClaimItem{UserID: c.UserID, Amount: c.Amount})
	}
	return &RedPacketDetailResp{
		RedPacket: toRedPacketResp(rp),
		Claims:    items,
	}, nil
}

// ListMine 我发出过的红包
func (s *RedPacketSrv) ListMine(ctx context.Context, req *RedPacketListReq) (interface{}, error) {
	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := NewRedPacketDao(ctx).ListMine(u.Id, req.LastID, req.PageSize)
	if err != nil {
		return nil, err
	}
	resp := &RedPacketListResp{List: make([]*RedPacketResp, 0, len(rows))}
	for _, r := range rows {
		resp.List = append(resp.List, toRedPacketResp(r))
	}
	if len(rows) > 0 {
		resp.LastID = rows[len(rows)-1].ID
	}
	return resp, nil
}

func toRedPacketResp(rp *RedPacket) *RedPacketResp {
	return &RedPacketResp{
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
