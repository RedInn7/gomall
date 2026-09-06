package initialize

import (
	"context"

	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/milvus"
	"github.com/RedInn7/gomall/service/search"
)

// InitMilvusCollection Milvus 未启用时直接跳过；启用则保证 product_vector collection 存在
func InitMilvusCollection(ctx context.Context) {
	if milvus.MilvusClient == nil {
		return
	}
	if err := milvus.EnsureProductVectorCollection(ctx); err != nil {
		util.LogrusObj.Errorf("EnsureProductVectorCollection failed: %v", err)
		return
	}
	search.SetProductVectorStore(search.MilvusProductVectorStore{})
	util.LogrusObj.Infoln("Milvus product_vector collection ready")
}
