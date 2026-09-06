package search

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/RedInn7/gomall/internal/product"
	util "github.com/RedInn7/gomall/pkg/utils/log"
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
	return handleProductChangedWith(ctx, ev, productIndexDeps{
		load:          product.NewProductDao(ctx).GetProductById,
		upsertKeyword: es.UpsertProduct,
		deleteKeyword: es.DeleteProduct,
		embed:         EmbedText,
	})
}

type productIndexDeps struct {
	load          func(uint) (*product.Product, error)
	upsertKeyword func(context.Context, *product.Product) error
	deleteKeyword func(context.Context, uint) error
	embed         embedFunc
}

func handleProductChangedWith(ctx context.Context, ev events.ProductChanged, deps productIndexDeps) error {
	store, vectorEnabled := getProductVectorStore()
	if ev.Op == "delete" {
		if err := deps.deleteKeyword(ctx, ev.ProductID); err != nil {
			return err
		}
		if vectorEnabled {
			return store.Delete(ctx, ev.ProductID)
		}
		return nil
	}
	p, err := deps.load(ev.ProductID)
	if err != nil || p == nil {
		return err
	}
	if err := deps.upsertKeyword(ctx, p); err != nil {
		return err
	}
	if !vectorEnabled {
		return nil
	}
	vec, err := deps.embed(ctx, productEmbeddingText(p))
	if err != nil {
		return err
	}
	return store.Upsert(ctx, p.ID, vec, p.CategoryID)
}

func productEmbeddingText(p *product.Product) string {
	return strings.Join([]string{strings.TrimSpace(p.Name), strings.TrimSpace(p.Title), strings.TrimSpace(p.Info)}, "\n")
}
