package redpacket

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/RedInn7/gomall/pkg/e"

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

// 业务错误，带码走统一错误出口。
var (
	ErrRedPacketNotExist     = e.New(e.ErrRedPacketNotExist)
	ErrRedPacketEnded        = e.New(e.ErrRedPacketEnded)
	ErrRedPacketExpired      = e.New(e.ErrRedPacketExpired)
	ErrRedPacketDrained      = e.New(e.ErrRedPacketDrained)
	ErrRedPacketAlreadyTaken = e.New(e.ErrRedPacketAlreadyTaken)
	ErrRedPacketForbidden    = e.New(e.ErrRedPacketForbidden)
)

// errRedPacketClosed 领取事务里发现红包已不在进行中（已被过期回收）。
var errRedPacketClosed = ErrRedPacketEnded

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
	shareCode, err := newShareCode()
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
		ShareCode: shareCode,
	}

	err = dao.NewDBClient(ctx).Transaction(func(tx *gorm.DB) error {
		if e := NewRedPacketDaoByDB(tx).Create(rp); e != nil {
			return e
		}
		// 发包人钱包扣 total 进平台清算 escrow，与红包主记录同事务原子落地。
		if _, e := settleSendTx(tx, rp.ID, u.Id, req.Total); e != nil {
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

	return toRedPacketResp(rp, true), nil
}

// newShareCode 16 位十六进制随机口令
func newShareCode() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// codeMatches 口令比对；老红包没有口令时 legacyOK 决定是否放行。
func codeMatches(rp *RedPacket, code string, legacyOK bool) bool {
	if rp.ShareCode == "" {
		return legacyOK
	}
	return subtle.ConstantTimeCompare([]byte(rp.ShareCode), []byte(code)) == 1
}

// Claim 抢红包：
//  0. 校验领取口令（id 自增可扫号，口令只在发包人分享出去的链接里）
//  1. Lua 原子 LPOP + 标记 claimed -> "amount:时间"
//  2. DB 事务（claimTx）：写 RedPacketClaim + remaining--（DB 剩余份额为准，归零即置 finished）+ 领取人钱包入账 amount
//     (同事务写台账，escrow 出账) + outbox(red_packet.claimed)
//  3. DB 事务失败 -> 先查 DB 确认这笔领取确实没落库，再 Saga 回滚 Lua (LPUSH 金额回 list, 撤销 claimed 标记)；
//     回滚 Lua 先比较标记，对账已接手补领（事务拖过 claimOrphanGrace）时不放回，避免 Redis 多出一份
//
// 在第 1、2 步之间崩溃 / 回滚 Lua 也失败时，claimed 标记成了孤儿：由 RunRedPacketClaimReconcile 按标记补领。
func (s *RedPacketSrv) Claim(ctx context.Context, req *RedPacketClaimReq) (interface{}, error) {
	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		return nil, err
	}

	rpDao := NewRedPacketDao(ctx)
	rp, err := rpDao.Get(req.ID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrRedPacketNotExist
	}
	if err != nil {
		return nil, err
	}
	if !codeMatches(rp, req.Code, true) {
		// 和「不存在」同样对待也行；单独给码方便前端提示重新打开分享链接
		return nil, ErrRedPacketForbidden
	}
	if rp.Status != RedPacketStatusActive {
		return nil, ErrRedPacketEnded
	}
	if time.Now().After(rp.ExpireAt) {
		return nil, ErrRedPacketExpired
	}

	claimedTTL := time.Until(rp.ExpireAt) + 24*time.Hour
	amount, err := cache.ClaimRedPacket(ctx, rp.ID, u.Id, claimedTTL)
	switch {
	case errors.Is(err, cache.ErrRedPacketDrained):
		return nil, ErrRedPacketDrained
	case errors.Is(err, cache.ErrRedPacketAlreadyTaken):
		return nil, ErrRedPacketAlreadyTaken
	case err != nil:
		return nil, err
	}

	if _, txErr := doClaimTx(rpDao.DB, rp.ID, u.Id, amount); txErr != nil {
		util.LogrusObj.Errorf("redpacket claim tx failed id=%d uid=%d err=%v", rp.ID, u.Id, txErr)
		// 回补前先问 DB：COMMIT 其实成功、只是客户端收到错误（提交结果未知）时，盲目回补会让 Redis 多出一份，
		// 后面的人多领走一份，托管被多付。DB 里已有这条领取就按成功返回，份额不放回。
		existing, qErr := rpDao.GetClaim(rp.ID, u.Id)
		switch {
		case qErr != nil:
			// 查不清楚就不回补：claimed 标记留着，对账任务按 DB 唯一索引补领或确认
			util.LogrusObj.Errorf("redpacket claim check failed id=%d uid=%d err=%v", rp.ID, u.Id, qErr)
		case existing != nil:
			return &RedPacketClaimResp{RedPacketID: rp.ID, Amount: existing.Amount}, nil
		case errors.Is(txErr, errRedPacketClosed):
			// 红包已结束时剩余金额已按 DB 退回，这份不能再塞回 Redis 给别人抢。
		default:
			rbErr := rollbackClaimCache(ctx, rp.ID, u.Id, amount)
			if errors.Is(rbErr, cache.ErrRedPacketClaimRepairing) {
				// 事务拖过了在途窗口，对账已按标记接手补领（份额没放回）：再问一次 DB，补上了就按成功返回。
				if c, e := rpDao.GetClaim(rp.ID, u.Id); e == nil && c != nil {
					return &RedPacketClaimResp{RedPacketID: rp.ID, Amount: c.Amount}, nil
				}
			} else if rbErr != nil {
				util.LogrusObj.Errorf("redpacket claim rollback failed id=%d uid=%d err=%v", rp.ID, u.Id, rbErr)
			}
		}
		return nil, txErr
	}

	return &RedPacketClaimResp{
		RedPacketID: rp.ID,
		Amount:      amount,
	}, nil
}

// 留成函数变量，单测模拟「提交结果未知」并给回滚计数（同 releaseRedPacketCache）。
var (
	doClaimTx          = claimTx
	rollbackClaimCache = cache.RollbackRedPacketClaim
)

// claimTx 一笔领取的 DB 事务：领取记录 + 剩余份数 + 入账 + outbox。Claim 和对账补领共用。
// uniq(red_packet_id, user_id) 保证同一用户只会成功一次，两条路径同时补也不会重复入账。
func claimTx(db *gorm.DB, rpID, uid uint, amount int64) (*RedPacketClaim, error) {
	claim := &RedPacketClaim{RedPacketID: rpID, UserID: uid, Amount: amount}
	err := db.Transaction(func(tx *gorm.DB) error {
		txDao := NewRedPacketDaoByDB(tx)
		if e := txDao.CreateClaim(claim); e != nil {
			return e
		}
		// DB 是剩余份额的权威：红包已被过期回收（剩余金额已退回发包人）时这里影响 0 行，整笔领取作废。
		n, e := txDao.DecrRemaining(rpID)
		if e != nil {
			return e
		}
		if n == 0 {
			return errRedPacketClosed
		}
		if e := txDao.MarkFinishedIfDrained(rpID); e != nil {
			return e
		}
		// 领取人钱包入账，escrow 出账；ref=领取记录 id（CreateClaim 后已回填自增主键）。
		if _, e := settleClaimTx(tx, claim.ID, uid, amount); e != nil {
			return e
		}
		return outbox.NewOutboxDaoByDB(tx).Insert(
			"red_packet", "RedPacketClaimed", "red_packet.claimed", rpID,
			events.RedPacketClaimed{
				RedPacketID: rpID,
				UserID:      uid,
				Amount:      amount,
			},
		)
	})
	if err != nil {
		return nil, err
	}
	return claim, nil
}

// Show 红包详情 + 领取明细。发包人、领过的人直接看；其他人要带口令（相当于「在群里」）。
func (s *RedPacketSrv) Show(ctx context.Context, req *RedPacketShowReq) (interface{}, error) {
	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		return nil, err
	}
	rpDao := NewRedPacketDao(ctx)
	rp, err := rpDao.Get(req.ID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrRedPacketNotExist
	}
	if err != nil {
		return nil, err
	}
	claims, err := rpDao.ListClaims(rp.ID)
	if err != nil {
		return nil, err
	}
	isSender := rp.UserID == u.Id
	allowed := isSender || codeMatches(rp, req.Code, false)
	items := make([]*RedPacketClaimItem, 0, len(claims))
	for _, c := range claims {
		allowed = allowed || c.UserID == u.Id
		items = append(items, &RedPacketClaimItem{UserID: c.UserID, Amount: c.Amount})
	}
	if !allowed {
		return nil, ErrRedPacketForbidden
	}
	return &RedPacketDetailResp{
		RedPacket: toRedPacketResp(rp, isSender),
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
		resp.List = append(resp.List, toRedPacketResp(r, true))
	}
	if len(rows) > 0 {
		resp.LastID = rows[len(rows)-1].ID
	}
	return resp, nil
}

// ListReceived 我领到的红包
func (s *RedPacketSrv) ListReceived(ctx context.Context, req *RedPacketListReq) (interface{}, error) {
	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		return nil, err
	}
	rpDao := NewRedPacketDao(ctx)
	claims, err := rpDao.ListReceived(u.Id, req.LastID, req.PageSize)
	if err != nil {
		return nil, err
	}
	ids := make([]uint, 0, len(claims))
	for _, c := range claims {
		ids = append(ids, c.RedPacketID)
	}
	rps, err := rpDao.ListByIDs(ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[uint]*RedPacket, len(rps))
	for _, rp := range rps {
		byID[rp.ID] = rp
	}
	resp := &RedPacketReceivedResp{List: make([]*RedPacketReceivedItem, 0, len(claims))}
	for _, c := range claims {
		item := &RedPacketReceivedItem{ClaimID: c.ID, RedPacketID: c.RedPacketID, Amount: c.Amount, ClaimedAt: c.CreatedAt.Unix()}
		if rp := byID[c.RedPacketID]; rp != nil {
			item.SenderID, item.Greeting = rp.UserID, rp.Greeting
		}
		resp.List = append(resp.List, item)
	}
	if len(claims) > 0 {
		resp.LastID = claims[len(claims)-1].ID
	}
	return resp, nil
}

// AdminReconcile 客服处理「显示已领却没到账」：立即对单个红包跑一遍 claimed 对账。
func (s *RedPacketSrv) AdminReconcile(ctx context.Context, req *RedPacketReconcileReq) (interface{}, error) {
	rp, err := NewRedPacketDao(ctx).Get(req.ID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrRedPacketNotExist
	}
	if err != nil {
		return nil, err
	}
	r, err := reconcileRedPacket(ctx, rp)
	if err != nil {
		return nil, err
	}
	return &RedPacketReconcileResp{Repaired: r.repaired, Refunded: r.refunded}, nil
}

func toRedPacketResp(rp *RedPacket, withCode bool) *RedPacketResp {
	code := ""
	if withCode {
		code = rp.ShareCode
	}
	return &RedPacketResp{
		ShareCode: code,
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
