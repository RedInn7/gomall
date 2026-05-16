package initialize

import (
	"context"

	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/es"
	"github.com/RedInn7/gomall/repository/rabbitmq"
	"github.com/RedInn7/gomall/service/search"
)

// InitSearch ES 不可用时静默跳过；RMQ 不可用时仅启用搜索但不接收增量更新
func InitSearch(ctx context.Context) {
	if es.EsClient == nil {
		util.LogrusObj.Warnln("ES 未初始化，跳过搜索索引启动")
		return
	}
	if err := es.EnsureProductIndex(ctx); err != nil {
		util.LogrusObj.Errorf("EnsureProductIndex failed: %v", err)
		return
	}
	if rabbitmq.GlobalRabbitMQ == nil {
		util.LogrusObj.Warnln("RMQ 未初始化，仅启用搜索查询，不接收增量索引事件")
		return
	}
	if err := search.StartProductIndexer(ctx); err != nil {
		util.LogrusObj.Errorf("StartProductIndexer failed: %v", err)
	} else {
		util.LogrusObj.Infoln("Product indexer started")
	}
}
