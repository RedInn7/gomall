package service

import (
	"context"

	"gorm.io/gorm"

	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/cache"
	"github.com/RedInn7/gomall/repository/db/dao"
	"github.com/RedInn7/gomall/repository/db/model"
	"github.com/RedInn7/gomall/service/events"
)

type RedPacketTaskService struct{}

// RunRedPacketExpireCheck 扫描过期未抢完的红包，回收剩余金额给发包人。
//  1. DB 查 status=active 且 expire_at <= now
//  2. Redis LPOP 全部剩余金额并清空 list
//  3. DB 事务：red_packet.status=refunded + outbox(red_packet.expired)
//  4. 下游钱包消费 red_packet.expired 回退发包人金额
func (s *RedPacketTaskService) RunRedPacketExpireCheck() {
	ctx := context.Background()
	rpDao := dao.NewRedPacketDao(ctx)
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
			txDao := dao.NewRedPacketDaoByDB(tx)
			if e := txDao.MarkStatus(rp.ID, model.RedPacketStatusRefunded); e != nil {
				return e
			}
			if left <= 0 {
				return nil
			}
			return dao.NewOutboxDaoByDB(tx).Insert(
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
