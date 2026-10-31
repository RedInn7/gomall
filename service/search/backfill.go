package search

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/RedInn7/gomall/internal/product"
	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/es"
)

var (
	backfillRunning    atomic.Bool
	errBackfillRunning = errors.New("商品索引回填正在进行中")
)

// StartBackfill 在后台跑 BackfillFromDB 并立即返回；同一时间只允许一个回填。
// 回填可能持续几分钟，不能挂在 HTTP 请求的 ctx 上，请求一返回 ctx 就被取消了。
func StartBackfill(ctx context.Context, batchSize int) error {
	if !backfillRunning.CompareAndSwap(false, true) {
		return errBackfillRunning
	}
	ctx = context.WithoutCancel(ctx)
	go func() {
		defer backfillRunning.Store(false)
		indexed, failed, err := BackfillFromDB(ctx, batchSize)
		if err != nil {
			util.LogrusObj.Errorf("backfill aborted indexed=%d failed=%v err=%v", indexed, failed, err)
			return
		}
		util.LogrusObj.Infof("backfill done indexed=%d failed=%v", indexed, failed)
	}()
	return nil
}

// BackfillFromDB 把 product 表全量导入 ES，Milvus 启用时同时写向量。
// 单条失败只记下 ID 继续往后走；只有建索引、翻页查库这类整体性错误才中断。
func BackfillFromDB(ctx context.Context, batchSize int) (indexed int, failed []uint, err error) {
	if err := es.EnsureProductIndex(ctx); err != nil {
		return 0, nil, err
	}
	store, vectorEnabled := getProductVectorStore()
	return backfill(ctx, batchSize, product.NewProductDao(ctx).ListAfterID, func(ctx context.Context, p *product.Product) error {
		if err := es.UpsertProduct(ctx, p); err != nil {
			return fmt.Errorf("es: %w", err)
		}
		if vectorEnabled {
			if err := syncProductVector(ctx, p, store, EmbedText); err != nil {
				return fmt.Errorf("vector: %w", err)
			}
		}
		return nil
	})
}

// backfill 按 id 游标分批遍历；游标在处理前就推进，单条失败不会让同一批被反复拉取空转
func backfill(ctx context.Context, batchSize int, list func(lastID uint, limit int) ([]*product.Product, error), index func(context.Context, *product.Product) error) (indexed int, failed []uint, err error) {
	if batchSize <= 0 {
		batchSize = 200
	}
	var lastID uint
	for {
		rows, err := list(lastID, batchSize)
		if err != nil {
			return indexed, failed, err
		}
		if len(rows) == 0 {
			return indexed, failed, nil
		}
		for _, p := range rows {
			lastID = p.ID
			if err := index(ctx, p); err != nil {
				util.LogrusObj.Errorf("backfill product=%d failed: %v", p.ID, err)
				failed = append(failed, p.ID)
				continue
			}
			indexed++
		}
		util.LogrusObj.Infof("backfill progress lastID=%d indexed=%d failed=%d", lastID, indexed, len(failed))
	}
}
