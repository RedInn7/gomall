package redpacket

import (
	"context"

	"gorm.io/gorm"

	"github.com/RedInn7/gomall/internal/shared/outbox"
	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/cache"
	"github.com/RedInn7/gomall/service/events"
)

type RedPacketTaskService struct{}

// RunRedPacketExpireCheck 扫描过期未抢完的红包，回收剩余金额给发包人。
//  1. DB 查 status=active 且 expire_at <= now
//  2. Redis LPOP 全部剩余金额并清空 list
//  3. DB 事务：red_packet.status=refunded + 剩余金额从 escrow 退回发包人钱包(同事务写台账) + outbox(red_packet.expired)
//
// 资金在第 3 步同事务原子落地（settleRefundTx）：发包人 credit / 清算 debit，
// ref=红包 id，幂等由台账唯一索引兜底；red_packet.expired 事件仅作通知。
func (s *RedPacketTaskService) RunRedPacketExpireCheck() {
	ctx := context.Background()
	rpDao := NewRedPacketDao(ctx)
	rows, err := rpDao.GetExpired(100)
	if err != nil {
		util.LogrusObj.Errorf("redpacket expire scan failed: %v", err)
		return
	}

	for _, rp := range rows {
		left, err := cache.ReleaseRedPacketLeft(ctx, rp.ID)
		if err != nil {
			util.LogrusObj.Errorf("release redpacket left failed id=%d err=%v", rp.ID, err)
			continue
		}

		txErr := rpDao.DB.Transaction(func(tx *gorm.DB) error {
			txDao := NewRedPacketDaoByDB(tx)
			if e := txDao.MarkStatus(rp.ID, RedPacketStatusRefunded); e != nil {
				return e
			}
			if left <= 0 {
				return nil
			}
			// 剩余金额从 escrow 退回发包人钱包，与状态切换同事务原子落地。
			if e := settleRefundTx(tx, rp.ID, rp.UserID, left); e != nil {
				return e
			}
			return outbox.NewOutboxDaoByDB(tx).Insert(
				"red_packet", "RedPacketExpired", "red_packet.expired", rp.ID,
				events.RedPacketExpired{
					RedPacketID: rp.ID,
					UserID:      rp.UserID,
					RefundTotal: left,
				},
			)
		})
		if txErr != nil {
			util.LogrusObj.Errorf("redpacket refund tx failed id=%d err=%v", rp.ID, txErr)
			continue
		}
		util.LogrusObj.Infof("redpacket id=%d 过期回收 refund=%d", rp.ID, left)
	}
}
