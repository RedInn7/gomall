package groupbuy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	amqp "github.com/rabbitmq/amqp091-go"
	"gorm.io/gorm/clause"

	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/db/dao"
	"github.com/RedInn7/gomall/repository/rabbitmq"
	"github.com/RedInn7/gomall/service/events"
)

// notifyQueue 拼团站内通知队列：参团告诉团长，成团 / 散团告诉全体成员。
// groupbuy.created 不订阅：团长自己刚开的团，同步响应已经告诉他了。
const notifyQueue = "groupbuy.notify"

var notifyRoutingKeys = []string{"groupbuy.joined", "groupbuy.success", "groupbuy.expired"}

// DispatchNotifyEvent 按 routing key 生成通知并落库。纯函数便于不连 RMQ 直接验证。
// 团已不存在视为脏事件放行；解析失败 / 未知 key 是毒消息。
func DispatchNotifyEvent(ctx context.Context, routingKey string, payload []byte) error {
	var (
		joined  events.GroupbuyJoined
		expired events.GroupbuyExpired
		success events.GroupbuySuccess
		decode  error
		groupID uint
	)
	switch routingKey {
	case "groupbuy.joined":
		decode = json.Unmarshal(payload, &joined)
		groupID = joined.GroupID
	case "groupbuy.success":
		decode = json.Unmarshal(payload, &success)
		groupID = success.GroupID
	case "groupbuy.expired":
		decode = json.Unmarshal(payload, &expired)
		groupID = expired.GroupID
	default:
		return fmt.Errorf("%w: unexpected routing key %q", errSettlePoisonMessage, routingKey)
	}
	if decode != nil || groupID == 0 {
		return fmt.Errorf("%w: bad %s payload %v", errSettlePoisonMessage, routingKey, decode)
	}

	gbDao := NewGroupbuyDao(ctx)
	g, err := gbDao.GetGroupByID(groupID)
	if err != nil {
		return err
	}
	if g == nil {
		util.LogrusObj.Warnf("groupbuy notify skip: group=%d not found", groupID)
		return nil
	}

	var notices []*GroupbuyNotice
	if routingKey == "groupbuy.joined" {
		if joined.UserID == g.LeaderID {
			return nil
		}
		notices = append(notices, &GroupbuyNotice{
			UserID: g.LeaderID, GroupID: groupID, Kind: NoticeKindJoined, RefID: joined.UserID,
			Content: fmt.Sprintf("有新成员参团，当前 %d/%d 人", joined.CurrentCount, joined.TargetCount),
		})
	} else {
		kind, content := NoticeKindSuccess, "拼团成功，等待商家发货"
		if routingKey == "groupbuy.expired" {
			kind, content = NoticeKindExpired, "拼团超时未成团，款项已原路退回钱包"
			if expired.Reason == dissolveReasonManual {
				content = "拼团已被平台关闭，款项已原路退回钱包"
			}
		}
		members, err := gbDao.ListMembers(groupID)
		if err != nil {
			return err
		}
		for _, m := range members {
			notices = append(notices, &GroupbuyNotice{UserID: m.UserID, GroupID: groupID, Kind: kind, Content: content})
		}
	}
	if len(notices) == 0 {
		return nil
	}
	return dao.NewDBClient(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&notices).Error
}

// ListMyNotices 用户的拼团通知，最新在前。
func (s *GroupbuySrv) ListMyNotices(ctx context.Context, userID uint, limit int) ([]*GroupbuyNotice, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	var rows []*GroupbuyNotice
	err := dao.NewDBClient(ctx).Where("user_id=?", userID).Order("id DESC").Limit(limit).Find(&rows).Error
	return rows, err
}

// StartNotifyConsumer 绑定参团 / 成团 / 散团事件并启动通知消费循环，错误处理与 settle 消费者一致。
func StartNotifyConsumer(ctx context.Context) error {
	if err := rabbitmq.InitDeadLetterTopology(); err != nil {
		return err
	}
	for _, key := range notifyRoutingKeys {
		if err := rabbitmq.BindDomainQueue(notifyQueue, key); err != nil {
			return err
		}
	}
	if err := rabbitmq.InitRetryQueue(notifyQueue, rabbitmq.SettleRetryDelays...); err != nil {
		return err
	}
	rabbitmq.SuperviseDomainConsumerWithRetry(notifyQueue, 16, func(d amqp.Delivery) {
		key := rabbitmq.OriginalRoutingKey(d)
		err := DispatchNotifyEvent(ctx, key, d.Body)
		if err == nil {
			_ = d.Ack(false)
			return
		}
		util.LogrusObj.Errorf("groupbuy notify handle key=%s err=%v", key, err)
		if errors.Is(err, errSettlePoisonMessage) {
			rabbitmq.RouteToDLQ(d, notifyQueue, key, true)
			return
		}
		rabbitmq.RetryToQueueOrDLQ(d, notifyQueue, key)
	})
	return nil
}
