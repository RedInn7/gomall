package milvus

import (
	"context"
	"errors"
	"fmt"

	"github.com/milvus-io/milvus-sdk-go/v2/client"
	"github.com/milvus-io/milvus-sdk-go/v2/entity"
)

const (
	// ProductVectorCollection 商品向量 collection 名
	ProductVectorCollection = "product_vector"

	// ProductVectorDim 向量维度。常见嵌入模型默认维度：
	// BGE-base / text-embedding-3-small 等多数模型为 768 dim
	ProductVectorDim = 768

	productVectorIDField       = "id"
	productVectorVectorField   = "vector"
	productVectorCategoryField = "category_id"

	// HNSW 索引参数
	hnswM              = 16
	hnswEfConstruction = 200
	hnswEfSearch       = 64
)

// ProductSearchHit 单条向量召回结果
type ProductSearchHit struct {
	ProductID uint
	Score     float32
}

// ErrMilvusNotInitialized Milvus 客户端未连接时返回
var ErrMilvusNotInitialized = errors.New("milvus client not initialized")

// productVectorSchema 构造 product_vector collection 的 schema
func productVectorSchema() *entity.Schema {
	return entity.NewSchema().
		WithName(ProductVectorCollection).
		WithDescription("gomall product embeddings for semantic recall").
		WithAutoID(false).
		WithField(
			entity.NewField().
				WithName(productVectorIDField).
				WithDataType(entity.FieldTypeInt64).
				WithIsPrimaryKey(true),
		).
		WithField(
			entity.NewField().
				WithName(productVectorVectorField).
				WithDataType(entity.FieldTypeFloatVector).
				WithDim(ProductVectorDim),
		).
		WithField(
			entity.NewField().
				WithName(productVectorCategoryField).
				WithDataType(entity.FieldTypeInt64),
		)
}

// EnsureProductVectorCollection 幂等：collection 不存在则创建并建 HNSW 索引、Load
func EnsureProductVectorCollection(ctx context.Context) error {
	if MilvusClient == nil {
		return ErrMilvusNotInitialized
	}

	has, err := MilvusClient.HasCollection(ctx, ProductVectorCollection)
	if err != nil {
		return fmt.Errorf("has collection: %w", err)
	}
	if !has {
		if err := MilvusClient.CreateCollection(ctx, productVectorSchema(), 1); err != nil {
			return fmt.Errorf("create collection: %w", err)
		}
	}

	idx, err := entity.NewIndexHNSW(entity.L2, hnswM, hnswEfConstruction)
	if err != nil {
		return fmt.Errorf("build hnsw index spec: %w", err)
	}

	existing, err := MilvusClient.DescribeIndex(ctx, ProductVectorCollection, productVectorVectorField)
	if err != nil || len(existing) == 0 {
		if err := MilvusClient.CreateIndex(ctx, ProductVectorCollection, productVectorVectorField, idx, false); err != nil {
			return fmt.Errorf("create index: %w", err)
		}
	}

	if err := MilvusClient.LoadCollection(ctx, ProductVectorCollection, false); err != nil {
		return fmt.Errorf("load collection: %w", err)
	}
	return nil
}

// UpsertProductVector 写入/更新单条商品向量
func UpsertProductVector(ctx context.Context, productID uint, vec []float32, categoryID uint) error {
	if len(vec) != ProductVectorDim {
		return fmt.Errorf("vector dim mismatch: got %d, want %d", len(vec), ProductVectorDim)
	}
	if MilvusClient == nil {
		return ErrMilvusNotInitialized
	}
	idCol := entity.NewColumnInt64(productVectorIDField, []int64{int64(productID)})
	vecCol := entity.NewColumnFloatVector(productVectorVectorField, ProductVectorDim, [][]float32{vec})
	catCol := entity.NewColumnInt64(productVectorCategoryField, []int64{int64(categoryID)})

	_, err := MilvusClient.Upsert(ctx, ProductVectorCollection, "", idCol, vecCol, catCol)
	if err != nil {
		return fmt.Errorf("upsert: %w", err)
	}
	return nil
}

// DeleteProductVector 按 product_id 删除单条
func DeleteProductVector(ctx context.Context, productID uint) error {
	if MilvusClient == nil {
		return ErrMilvusNotInitialized
	}
	ids := entity.NewColumnInt64(productVectorIDField, []int64{int64(productID)})
	if err := MilvusClient.DeleteByPks(ctx, ProductVectorCollection, "", ids); err != nil {
		return fmt.Errorf("delete: %w", err)
	}
	return nil
}

// SearchProductVector 语义召回 topK；categoryFilter 非空时按 category_id 过滤
func SearchProductVector(ctx context.Context, queryVec []float32, topK int, categoryFilter *uint) ([]ProductSearchHit, error) {
	if len(queryVec) != ProductVectorDim {
		return nil, fmt.Errorf("query vector dim mismatch: got %d, want %d", len(queryVec), ProductVectorDim)
	}
	if MilvusClient == nil {
		return nil, ErrMilvusNotInitialized
	}
	if topK <= 0 {
		topK = 10
	}

	sp, err := entity.NewIndexHNSWSearchParam(hnswEfSearch)
	if err != nil {
		return nil, fmt.Errorf("build search param: %w", err)
	}

	expr := ""
	if categoryFilter != nil {
		expr = fmt.Sprintf("%s == %d", productVectorCategoryField, *categoryFilter)
	}

	results, err := MilvusClient.Search(
		ctx,
		ProductVectorCollection,
		[]string{},
		expr,
		[]string{productVectorIDField},
		[]entity.Vector{entity.FloatVector(queryVec)},
		productVectorVectorField,
		entity.L2,
		topK,
		sp,
	)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}

	return flattenSearchResults(results), nil
}

func flattenSearchResults(results []client.SearchResult) []ProductSearchHit {
	hits := make([]ProductSearchHit, 0)
	for _, r := range results {
		if r.IDs == nil {
			continue
		}
		idCol, ok := r.IDs.(*entity.ColumnInt64)
		if !ok {
			continue
		}
		ids := idCol.Data()
		for i, id := range ids {
			var score float32
			if i < len(r.Scores) {
				score = r.Scores[i]
			}
			hits = append(hits, ProductSearchHit{
				ProductID: uint(id),
				Score:     score,
			})
		}
	}
	return hits
}
