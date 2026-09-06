package search

import (
	"context"
	"sync"
)

// Hit 表示向量检索返回的单条命中
type Hit struct {
	ID    int64
	Score float32
}

// MilvusSearcher 抽象向量召回端，便于 mock；真实实现由 repository/milvus 提供后通过 SetSearcher 注入
type MilvusSearcher interface {
	Search(ctx context.Context, vec []float32, topK int, categoryID *uint) ([]Hit, error)
}

// ProductVectorStore 是商品语义索引的完整端口。生产实现写入并查询 Milvus，
// nil 实现让未配置 Milvus 的环境继续使用关键词搜索。
type ProductVectorStore interface {
	MilvusSearcher
	Upsert(ctx context.Context, productID uint, vec []float32, categoryID uint) error
	Delete(ctx context.Context, productID uint) error
}

// nopMilvusSearcher 默认实现，未接入真 Milvus 时返回空结果
type nopMilvusSearcher struct{}

func (nopMilvusSearcher) Search(ctx context.Context, vec []float32, topK int, categoryID *uint) ([]Hit, error) {
	return nil, nil
}

type nopProductVectorStore struct{ nopMilvusSearcher }

func (nopProductVectorStore) Upsert(context.Context, uint, []float32, uint) error { return nil }
func (nopProductVectorStore) Delete(context.Context, uint) error                  { return nil }

var (
	searcherMu         sync.RWMutex
	searcher           MilvusSearcher     = nopMilvusSearcher{}
	vectorStore        ProductVectorStore = nopProductVectorStore{}
	vectorStoreEnabled bool
)

// GetSearcher 返回当前注册的 Milvus searcher
func GetSearcher() MilvusSearcher {
	searcherMu.RLock()
	defer searcherMu.RUnlock()
	return searcher
}

func getProductVectorStore() (ProductVectorStore, bool) {
	searcherMu.RLock()
	defer searcherMu.RUnlock()
	return vectorStore, vectorStoreEnabled
}

// SetSearcher 注入真实 Milvus searcher，nil 时回退到 nop
func SetSearcher(s MilvusSearcher) {
	searcherMu.Lock()
	defer searcherMu.Unlock()
	if s == nil {
		searcher = nopMilvusSearcher{}
		return
	}
	searcher = s
}

// SetProductVectorStore 原子装配 Milvus 的读写两端；nil 会关闭向量链路。
func SetProductVectorStore(store ProductVectorStore) {
	searcherMu.Lock()
	defer searcherMu.Unlock()
	if store == nil {
		searcher = nopMilvusSearcher{}
		vectorStore = nopProductVectorStore{}
		vectorStoreEnabled = false
		return
	}
	searcher = store
	vectorStore = store
	vectorStoreEnabled = true
}
