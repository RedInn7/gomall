package initialize

import (
	"context"
	"fmt"
	"time"

	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/es"
	"github.com/RedInn7/gomall/repository/rabbitmq"
	"github.com/RedInn7/gomall/service/search"
)

// InitSearch ES 未配置时静默跳过；ES 暂时连不上时在后台按退避重试，直到建好索引并启动增量消费者，
// 否则启动那一刻 ES 不可达，增量索引就再也不会启动。RMQ 不可用时仅启用搜索但不接收增量更新。
func InitSearch(ctx context.Context) {
	if es.EsClient == nil {
		util.LogrusObj.Warnln("ES 未初始化，跳过搜索索引启动")
		return
	}
	go func() {
		for wait := time.Second; ; wait = min(wait*2, time.Minute) {
			err := startSearchIndexing(ctx)
			if err == nil {
				return
			}
			util.LogrusObj.Errorf("搜索索引启动失败，%s 后重试: %v", wait, err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
		}
	}()
}

func startSearchIndexing(ctx context.Context) error {
	if err := es.EnsureProductIndex(ctx); err != nil {
		return fmt.Errorf("EnsureProductIndex: %w", err)
	}
	if rabbitmq.GlobalRabbitMQ == nil {
		util.LogrusObj.Warnln("RMQ 未初始化，仅启用搜索查询，不接收增量索引事件")
		return nil
	}
	if err := search.StartProductIndexer(ctx); err != nil {
		return fmt.Errorf("StartProductIndexer: %w", err)
	}
	util.LogrusObj.Infoln("Product indexer started")
	return nil
}
