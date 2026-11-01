package redpacket

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"github.com/RedInn7/gomall/internal/shared/outbox"
	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/cache"
	"github.com/RedInn7/gomall/repository/db/dao"
	"github.com/RedInn7/gomall/service/events"
)

type RedPacketTaskService struct{}

// releaseRedPacketCache 退款提交后清掉 Redis 里剩下的份额。留成函数变量，单测替换（同 preorder.nowFn）。
var releaseRedPacketCache = cache.ReleaseRedPacketLeft

// RunRedPacketExpireCheck 扫描已过期、DB 计算仍有剩余金额的红包，把剩余金额退回发包人。
// 剩余金额以 DB 为准（Total - 已领总额），不读 Redis：list 可能已被清空、过期，或发包后根本没写进去。
func (s *RedPacketTaskService) RunRedPacketExpireCheck() {
	ctx := context.Background()
	rows, err := NewRedPacketDao(ctx).GetExpired(100)
	if err != nil {
		util.LogrusObj.Errorf("redpacket expire scan failed: %v", err)
		return
	}

	for _, rp := range rows {
		// 先把孤儿领取补上，再按 DB 退剩余：否则抢到了的那份会被退给发包人。
		r, err := reconcileRedPacket(ctx, rp)
		if err != nil {
			util.LogrusObj.Errorf("redpacket reconcile before refund failed id=%d err=%v", rp.ID, err)
		}
		if (err != nil || r.pending) && time.Since(rp.ExpireAt) < refundHoldForReconcile {
			continue // 留一段时间给对账补领；超时照退，钱回发包人不会丢
		}
		if r.refunded {
			continue
		}
		left, err := refundRedPacketLeft(ctx, rp.ID)
		if err != nil {
			util.LogrusObj.Errorf("redpacket refund tx failed id=%d err=%v", rp.ID, err)
			continue
		}
		util.LogrusObj.Infof("redpacket id=%d 过期回收 refund=%d", rp.ID, left)
	}
}

// refundRedPacketLeft 把红包剩余金额退回发包人，幂等。过期回收与发包后 Redis 准备失败的补偿共用。
//  1. 一个事务内 FOR UPDATE 锁红包行，已 refunded / finished 直接返回
//  2. 剩余金额 = Total - SUM(已领金额)，标记 refunded，从 escrow 退回发包人钱包（同事务写台账）+ outbox(red_packet.expired)
//  3. 提交后再清 Redis 剩余份额，清理失败只记日志：领取事务会因红包已 refunded 被拒
//
// 领取事务的 DecrRemaining 同样要拿这行的锁，二者串行，剩余金额不会既退给发包人又被领走。
func refundRedPacketLeft(ctx context.Context, rpID uint) (int64, error) {
	var left int64
	err := dao.NewDBClient(ctx).Transaction(func(tx *gorm.DB) error {
		txDao := NewRedPacketDaoByDB(tx)
		// 必须是事务第一条语句：MySQL RR 下加锁读不建快照，下面的 SUM 才在拿到锁之后建快照、
		// 看得到刚提交的领取。往前面插任何普通读，快照会提前建立，SUM 漏掉并发提交的领取，同一份钱退两次。
		// TestClaimAndRefundRaceConservesMoney 守着这条。
		rp, err := txDao.GetForUpdate(rpID)
		if err != nil {
			return err
		}
		if rp.Status == RedPacketStatusRefunded || rp.Status == RedPacketStatusFinished {
			return nil
		}
		claimed, err := txDao.SumClaimed(rpID)
		if err != nil {
			return err
		}
		left = rp.Total - claimed
		if err = txDao.MarkStatus(rpID, RedPacketStatusRefunded); err != nil {
			return err
		}
		if left <= 0 {
			return nil
		}
		// 剩余金额从 escrow 退回发包人钱包，与状态切换同事务原子落地。
		if _, err = settleRefundTx(tx, rpID, rp.UserID, left); err != nil {
			return err
		}
		return outbox.NewOutboxDaoByDB(tx).Insert(
			"red_packet", "RedPacketExpired", "red_packet.expired", rpID,
			events.RedPacketExpired{
				RedPacketID: rpID,
				UserID:      rp.UserID,
				RefundTotal: left,
			},
		)
	})
	if err != nil {
		return 0, err
	}
	if _, e := releaseRedPacketCache(ctx, rpID); e != nil {
		util.LogrusObj.Errorf("release redpacket cache failed id=%d err=%v", rpID, e)
	}
	return left, nil
}

const (
	// claimOrphanGrace Lua 抢到后 DB 事务还在跑的正常在途时间，比它新的 claimed 标记不当孤儿处理。
	claimOrphanGrace = time.Minute
	// refundHoldForReconcile 过期后还有孤儿没补上时，退款最多等这么久。
	refundHoldForReconcile = time.Hour
)

type reconcileResult struct {
	repaired int  // 补领的孤儿数
	refunded bool // 份额池丢失，已提前退款
	pending  bool // 还有在途或补领失败的标记，本轮不能下结论
}

// RunRedPacketClaimReconcile 对账 Redis claimed 标记和 DB 领取记录：
//   - 有标记没记录、且超过在途窗口：Lua 抢到了、DB 没落（崩溃在两步之间 / 回滚也失败），按标记金额补领入账。
//     用户抢到的就是这一份，补给他而不是退给发包人；补领走 claimTx，唯一索引兜底，和原请求撞上也只成一次。
//   - 份额 LIST 已不存在但 DB 还剩份数（发包后写 Redis 失败且补偿也失败、Redis 丢数据）：没人还能抢到，
//     不必等到过期，立即按 DB 退回发包人。
func (s *RedPacketTaskService) RunRedPacketClaimReconcile() {
	ctx := context.Background()
	rows, err := NewRedPacketDao(ctx).ListActive(500)
	if err != nil {
		util.LogrusObj.Errorf("redpacket reconcile scan failed: %v", err)
		return
	}
	for _, rp := range rows {
		if _, err := reconcileRedPacket(ctx, rp); err != nil {
			util.LogrusObj.Errorf("redpacket reconcile failed id=%d err=%v", rp.ID, err)
		}
	}
}

func reconcileRedPacket(ctx context.Context, rp *RedPacket) (reconcileResult, error) {
	var r reconcileResult
	marks, err := cache.ListRedPacketClaimed(ctx, rp.ID)
	if err != nil {
		return r, err
	}
	rpDao := NewRedPacketDao(ctx)
	claims, err := rpDao.ListClaims(rp.ID)
	if err != nil {
		return r, err
	}
	inDB := make(map[uint]bool, len(claims))
	for _, c := range claims {
		inDB[c.UserID] = true
	}
	for uid, m := range marks {
		if inDB[uid] {
			continue
		}
		if time.Since(m.At) < claimOrphanGrace {
			r.pending = true
			continue
		}
		// 先把标记改成「补领中」再补：原请求的 Saga 回滚看到它就不会再把这份放回 Redis。
		// 改不动说明原请求已回滚（份额已放回）或用户撤销后又重抢了，这一份不归对账管。
		locked, err := cache.LockRedPacketClaimForRepair(ctx, rp.ID, uid, m.Raw)
		if err != nil {
			r.pending = true
			util.LogrusObj.Errorf("redpacket 补领加锁失败 id=%d uid=%d err=%v", rp.ID, uid, err)
			continue
		}
		if !locked {
			continue
		}
		if _, err := claimTx(rpDao.DB, rp.ID, uid, m.Amount); err != nil {
			if errors.Is(err, errRedPacketClosed) {
				break // 已回收 / 抢完，剩下的标记补不了也不用补
			}
			if c, qErr := rpDao.GetClaim(rp.ID, uid); qErr == nil && c != nil {
				continue // 原请求刚好提交了
			}
			util.LogrusObj.Errorf("redpacket 补领失败 id=%d uid=%d amount=%d err=%v", rp.ID, uid, m.Amount, err)
			r.pending = true
			continue
		}
		r.repaired++
		util.LogrusObj.Warnf("redpacket 补领孤儿领取 id=%d uid=%d amount=%d", rp.ID, uid, m.Amount)
	}
	if r.pending || time.Since(rp.CreatedAt) < claimOrphanGrace {
		return r, nil
	}

	exists, err := cache.RedPacketPoolExists(ctx, rp.ID)
	if err != nil || exists {
		return r, err
	}
	fresh, err := rpDao.Get(rp.ID)
	if err != nil {
		return r, err
	}
	if fresh.Status != RedPacketStatusActive || fresh.Remaining == 0 {
		return r, nil
	}
	left, err := refundRedPacketLeft(ctx, rp.ID)
	if err != nil {
		return r, err
	}
	r.refunded = true
	util.LogrusObj.Warnf("redpacket 份额池丢失，提前退款 id=%d remaining=%d refund=%d", rp.ID, fresh.Remaining, left)
	return r, nil
}
