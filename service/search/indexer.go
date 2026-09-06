package search

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/RedInn7/gomall/internal/product"
	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/es"
	"github.com/RedInn7/gomall/repository/rabbitmq"
	"github.com/RedInn7/gomall/service/events"
)

const (
	indexerQueue       = "search.product.indexer"
	vectorIndexerQueue = "search.product.vector-indexer"
	indexerRetryDelay  = 3 * time.Second
)

// StartProductIndexer 使用独立队列维护 ES 关键词索引。
func StartProductIndexer(ctx context.Context) error {
	return startProductIndexConsumer(ctx, indexerQueue, handleKeywordProductChanged)
}

// StartProductVectorIndexer 使用独立队列维护 Milvus；ES 故障不会阻塞向量索引。
func StartProductVectorIndexer(ctx context.Context) error {
	return startProductIndexConsumer(ctx, vectorIndexerQueue, handleVectorProductChanged)
}

type productChangedHandler func(context.Context, events.ProductChanged) error

func startProductIndexConsumer(ctx context.Context, queue string, handler productChangedHandler) error {
	if err := rabbitmq.InitDeadLetterTopology(); err != nil {
		return err
	}
	if err := rabbitmq.BindDomainQueue(queue, "product.changed"); err != nil {
		return err
	}
	if err := rabbitmq.InitRetryQueue(queue, indexerRetryDelay); err != nil {
		return err
	}
	rabbitmq.SuperviseDomainConsumerWithRetry(queue, 32, func(d amqp.Delivery) {
		var ev events.ProductChanged
		if err := json.Unmarshal(d.Body, &ev); err != nil || ev.ProductID == 0 {
			util.LogrusObj.Errorf("product indexer parse queue=%s err=%v", queue, err)
			rabbitmq.RouteToDLQ(d, queue, d.RoutingKey, true)
			return
		}
		if err := handler(ctx, ev); err != nil {
			util.LogrusObj.Errorf("product indexer handle queue=%s product=%d op=%s err=%v", queue, ev.ProductID, ev.Op, err)
			rabbitmq.RetryToQueueOrDLQ(d, queue, d.RoutingKey)
			return
		}
		_ = d.Ack(false)
	})
	return nil
}

func handleKeywordProductChanged(ctx context.Context, ev events.ProductChanged) error {
	return handleKeywordProductChangedWith(ctx, ev, product.NewProductDao(ctx).GetProductById, es.UpsertProduct, es.DeleteProduct)
}

func handleKeywordProductChangedWith(ctx context.Context, ev events.ProductChanged, load func(uint) (*product.Product, error), upsert func(context.Context, *product.Product) error, deleteProduct func(context.Context, uint) error) error {
	if ev.Op == "delete" {
		return deleteProduct(ctx, ev.ProductID)
	}
	p, err := load(ev.ProductID)
	if err != nil || p == nil {
		return err
	}
	return upsert(ctx, p)
}

func handleVectorProductChanged(ctx context.Context, ev events.ProductChanged) error {
	store, enabled := getProductVectorStore()
	if !enabled {
		return nil
	}
	return handleVectorProductChangedWith(ctx, ev, product.NewProductDao(ctx).GetProductById, EmbedText, store)
}

func handleVectorProductChangedWith(ctx context.Context, ev events.ProductChanged, load func(uint) (*product.Product, error), embed embedFunc, store ProductVectorStore) error {
	if ev.Op == "delete" {
		return store.Delete(ctx, ev.ProductID)
	}
	p, err := load(ev.ProductID)
	if err != nil || p == nil {
		return err
	}
	return syncProductVector(ctx, p, store, embed)
}

func syncProductVector(ctx context.Context, p *product.Product, store ProductVectorStore, embed embedFunc) error {
	if !p.OnSale {
		return store.Delete(ctx, p.ID)
	}
	vec, err := embed(ctx, productEmbeddingText(p))
	if err != nil {
		return err
	}
	return store.Upsert(ctx, p.ID, vec, p.CategoryID)
}

func productEmbeddingText(p *product.Product) string {
	return strings.Join([]string{strings.TrimSpace(p.Name), strings.TrimSpace(p.Title), strings.TrimSpace(p.Info)}, "\n")
}
