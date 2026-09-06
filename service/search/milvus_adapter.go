package search

import (
	"context"

	"github.com/RedInn7/gomall/repository/milvus"
)

// MilvusProductVectorStore 把搜索领域端口接到 repository/milvus。
type MilvusProductVectorStore struct{}

func (MilvusProductVectorStore) Search(ctx context.Context, vec []float32, topK int, categoryID *uint) ([]Hit, error) {
	rows, err := milvus.SearchProductVector(ctx, vec, topK, categoryID)
	if err != nil {
		return nil, err
	}
	hits := make([]Hit, 0, len(rows))
	for _, row := range rows {
		hits = append(hits, Hit{ID: int64(row.ProductID), Score: row.Score})
	}
	return hits, nil
}

func (MilvusProductVectorStore) Upsert(ctx context.Context, productID uint, vec []float32, categoryID uint) error {
	return milvus.UpsertProductVector(ctx, productID, vec, categoryID)
}

func (MilvusProductVectorStore) Delete(ctx context.Context, productID uint) error {
	return milvus.DeleteProductVector(ctx, productID)
}
