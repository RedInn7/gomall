package service

import (
	"context"
	"encoding/json"

	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/service/events"
)

// HandleRedPacketClaimedEvent 消费 red_packet.claimed 事件并把金额入账到领取人钱包。
// 钱包余额以用户支付密码 AES 加密 (见 service/payment.go)，下游钱包服务持有密钥
// 体系；此处只完成事件解码与日志埋点，实际入账由下游钱包消费者落地。
//   - 幂等性由 outbox 投递 + 钱包侧业务 ID 去重保证
//   - 失败由 outbox 重试 + 死信兜底
func HandleRedPacketClaimedEvent(_ context.Context, payload []byte) error {
	var evt events.RedPacketClaimed
	if err := json.Unmarshal(payload, &evt); err != nil {
		util.LogrusObj.Errorf("decode red_packet.claimed payload failed: %v", err)
		return err
	}
	util.LogrusObj.Infof("redpacket claimed: id=%d uid=%d amount=%d (待下游钱包入账)",
		evt.RedPacketID, evt.UserID, evt.Amount)
	return nil
}

// HandleRedPacketExpiredEvent 红包过期回收：发包人钱包加回剩余金额。
// 同上，实际入账由下游钱包服务消费 red_packet.expired 事件。
func HandleRedPacketExpiredEvent(_ context.Context, payload []byte) error {
	var evt events.RedPacketExpired
	if err := json.Unmarshal(payload, &evt); err != nil {
		util.LogrusObj.Errorf("decode red_packet.expired payload failed: %v", err)
		return err
	}
	util.LogrusObj.Infof("redpacket expired: id=%d uid=%d refund=%d (待下游钱包回退)",
		evt.RedPacketID, evt.UserID, evt.RefundTotal)
	return nil
}
