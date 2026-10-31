package redpacket

import (
	"context"

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
		left, err := refundRedPacketLeft(ctx, rp.ID)
		if err != nil {
			util.LogrusObj.Errorf("redpacket refund tx failed id=%d err=%v", rp.ID, err)
			continue
		}
		util.LogrusObj.Infof("redpacket id=%d 过期回收 refund=%d", rp.ID, left)
	}
}

// refundRedPacketLeft 把红包剩余金额退回发包人，幂等。过期回收与发包后 Redis 准备失败的补偿共用。
//  1. 一个事务内 FOR UPDATE 锁红包行，已 refunded 直接返回
//  2. 剩余金额 = Total - SUM(已领金额)，标记 refunded，从 escrow 退回发包人钱包（同事务写台账）+ outbox(red_packet.expired)
//  3. 提交后再清 Redis 剩余份额，清理失败只记日志：领取事务会因红包已 refunded 被拒
//
// 领取事务的 DecrRemaining 同样要拿这行的锁，二者串行，剩余金额不会既退给发包人又被领走。
func refundRedPacketLeft(ctx context.Context, rpID uint) (int64, error) {
	var left int64
	err := dao.NewDBClient(ctx).Transaction(func(tx *gorm.DB) error {
		txDao := NewRedPacketDaoByDB(tx)
		rp, err := txDao.GetForUpdate(rpID)
		if err != nil {
			return err
		}
		if rp.Status == RedPacketStatusRefunded {
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
		if err = settleRefundTx(tx, rpID, rp.UserID, left); err != nil {
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
