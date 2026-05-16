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

// nopMilvusSearcher 默认实现，未接入真 Milvus 时返回空结果
type nopMilvusSearcher struct{}

func (nopMilvusSearcher) Search(ctx context.Context, vec []float32, topK int, categoryID *uint) ([]Hit, error) {
	return nil, nil
}

var (
	searcherMu sync.RWMutex
	searcher   MilvusSearcher = nopMilvusSearcher{}
)

// GetSearcher 返回当前注册的 Milvus searcher
func GetSearcher() MilvusSearcher {
	searcherMu.RLock()
	defer searcherMu.RUnlock()
	return searcher
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
