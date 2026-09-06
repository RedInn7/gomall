package initialize

import (
	"context"

	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/milvus"
	"github.com/RedInn7/gomall/repository/rabbitmq"
	"github.com/RedInn7/gomall/service/search"
)

// InitMilvusCollection Milvus 未启用时直接跳过；启用则保证 product_vector collection 存在
func InitMilvusCollection(ctx context.Context) {
	if milvus.MilvusClient == nil {
		return
	}
	contract := search.CurrentEmbeddingContract()
	if err := milvus.ConfigureProductVectorCollection(contract.CollectionName()); err != nil {
		util.LogrusObj.Errorf("ConfigureProductVectorCollection failed: %v", err)
		return
	}
	if err := milvus.EnsureProductVectorCollection(ctx); err != nil {
		util.LogrusObj.Errorf("EnsureProductVectorCollection failed: %v", err)
		return
	}
	search.SetProductVectorStore(search.MilvusProductVectorStore{})
	util.LogrusObj.Infof("Milvus collection ready name=%s embedding=%s", contract.CollectionName(), contract.Fingerprint())
	if rabbitmq.GlobalRabbitMQ == nil {
		util.LogrusObj.Warnln("RMQ 未初始化，Milvus 查询已启用，但不接收增量向量事件")
		return
	}
	if err := search.StartProductVectorIndexer(ctx); err != nil {
		util.LogrusObj.Errorf("StartProductVectorIndexer failed: %v", err)
		return
	}
	util.LogrusObj.Infoln("Milvus product vector indexer started")
}
