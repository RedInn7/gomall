package search

import (
	"context"
	"encoding/json"

	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/db/dao"
	"github.com/RedInn7/gomall/repository/es"
	"github.com/RedInn7/gomall/repository/rabbitmq"
	"github.com/RedInn7/gomall/service/events"
)

const indexerQueue = "search.product.indexer"

// StartProductIndexer 绑定 product.changed 并启动消费者，把产品变更同步到 ES
func StartProductIndexer(ctx context.Context) error {
	if err := rabbitmq.BindDomainQueue(indexerQueue, "product.changed"); err != nil {
		return err
	}
	ch, err := rabbitmq.GlobalRabbitMQ.Channel()
	if err != nil {
		return err
	}
	if err := ch.Qos(32, 0, false); err != nil {
		return err
	}
	msgs, err := ch.Consume(indexerQueue, "", false, false, false, false, nil)
	if err != nil {
		return err
	}
	go func() {
		for d := range msgs {
			var ev events.ProductChanged
			if err := json.Unmarshal(d.Body, &ev); err != nil {
				util.LogrusObj.Errorln("indexer parse event:", err)
				_ = d.Nack(false, false)
				continue
			}
			if err := handleProductChanged(ctx, ev); err != nil {
				util.LogrusObj.Errorf("indexer handle product=%d op=%s err=%v", ev.ProductID, ev.Op, err)
				_ = d.Nack(false, true)
				continue
			}
			_ = d.Ack(false)
		}
	}()
	return nil
}

func handleProductChanged(ctx context.Context, ev events.ProductChanged) error {
	if ev.Op == "delete" {
		return es.DeleteProduct(ctx, ev.ProductID)
	}
	p, err := dao.NewProductDao(ctx).GetProductById(ev.ProductID)
	if err != nil || p == nil {
		return err
	}
	return es.UpsertProduct(ctx, p)
}
