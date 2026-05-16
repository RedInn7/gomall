package milvus

import (
	"context"
	"errors"
	"testing"

	"github.com/milvus-io/milvus-sdk-go/v2/entity"
)

func TestProductVectorSchema(t *testing.T) {
	sch := productVectorSchema()
	if sch.CollectionName != ProductVectorCollection {
		t.Fatalf("collection name = %q, want %q", sch.CollectionName, ProductVectorCollection)
	}
	if sch.AutoID {
		t.Fatalf("AutoID should be false; primary key 来自业务 product.ID")
	}
	if len(sch.Fields) != 3 {
		t.Fatalf("field count = %d, want 3", len(sch.Fields))
	}

	byName := map[string]*entity.Field{}
	for _, f := range sch.Fields {
		byName[f.Name] = f
	}

	idField, ok := byName[productVectorIDField]
	if !ok {
		t.Fatalf("missing id field")
	}
	if !idField.PrimaryKey {
		t.Fatalf("id must be primary key")
	}
	if idField.DataType != entity.FieldTypeInt64 {
		t.Fatalf("id data type = %v, want Int64", idField.DataType)
	}

	vecField, ok := byName[productVectorVectorField]
	if !ok {
		t.Fatalf("missing vector field")
	}
	if vecField.DataType != entity.FieldTypeFloatVector {
		t.Fatalf("vector data type = %v, want FloatVector", vecField.DataType)
	}
	if dim := vecField.TypeParams[entity.TypeParamDim]; dim != "768" {
		t.Fatalf("vector dim = %q, want 768", dim)
	}

	catField, ok := byName[productVectorCategoryField]
	if !ok {
		t.Fatalf("missing category_id field")
	}
	if catField.DataType != entity.FieldTypeInt64 {
		t.Fatalf("category_id data type = %v, want Int64", catField.DataType)
	}
	if catField.PrimaryKey {
		t.Fatalf("category_id should not be primary key")
	}
}

func TestHNSWIndexParams(t *testing.T) {
	idx, err := entity.NewIndexHNSW(entity.L2, hnswM, hnswEfConstruction)
	if err != nil {
		t.Fatalf("build hnsw index spec: %v", err)
	}
	if idx.IndexType() != "HNSW" {
		t.Fatalf("index type = %v, want HNSW", idx.IndexType())
	}

	sp, err := entity.NewIndexHNSWSearchParam(hnswEfSearch)
	if err != nil {
		t.Fatalf("build hnsw search param: %v", err)
	}
	if sp == nil {
		t.Fatalf("search param is nil")
	}
}

func TestUpsertProductVectorWithoutClient(t *testing.T) {
	saved := MilvusClient
	MilvusClient = nil
	defer func() { MilvusClient = saved }()

	err := UpsertProductVector(context.Background(), 1, make([]float32, ProductVectorDim), 1)
	if !errors.Is(err, ErrMilvusNotInitialized) {
		t.Fatalf("err = %v, want ErrMilvusNotInitialized", err)
	}

	err = DeleteProductVector(context.Background(), 1)
	if !errors.Is(err, ErrMilvusNotInitialized) {
		t.Fatalf("err = %v, want ErrMilvusNotInitialized", err)
	}

	_, err = SearchProductVector(context.Background(), make([]float32, ProductVectorDim), 5, nil)
	if !errors.Is(err, ErrMilvusNotInitialized) {
		t.Fatalf("err = %v, want ErrMilvusNotInitialized", err)
	}
}

func TestUpsertProductVectorDimMismatch(t *testing.T) {
	// dim 校验先于 client nil 校验，确保编程错误不会被 "client 未初始化" 掩盖
	err := UpsertProductVector(context.Background(), 1, []float32{0.1, 0.2}, 0)
	if err == nil {
		t.Fatalf("expected dim mismatch error")
	}
	if errors.Is(err, ErrMilvusNotInitialized) {
		t.Fatalf("dim mismatch should not be reported as ErrMilvusNotInitialized")
	}
}

func TestSearchProductVectorDimMismatch(t *testing.T) {
	_, err := SearchProductVector(context.Background(), []float32{0.1, 0.2}, 5, nil)
	if err == nil {
		t.Fatalf("expected dim mismatch error")
	}
	if errors.Is(err, ErrMilvusNotInitialized) {
		t.Fatalf("dim mismatch should not be reported as ErrMilvusNotInitialized")
	}
}

func TestInitMilvusEmptyAddr(t *testing.T) {
	t.Setenv("MILVUS_ADDR", "")
	saved := MilvusClient
	MilvusClient = nil
	defer func() { MilvusClient = saved }()

	if err := InitMilvus(); err != nil {
		t.Fatalf("InitMilvus with empty addr returned err: %v", err)
	}
	if MilvusClient != nil {
		t.Fatalf("MilvusClient should remain nil when MILVUS_ADDR is empty")
	}
}

func TestEnsureProductVectorCollectionRequiresClient(t *testing.T) {
	saved := MilvusClient
	MilvusClient = nil
	defer func() { MilvusClient = saved }()

	err := EnsureProductVectorCollection(context.Background())
	if !errors.Is(err, ErrMilvusNotInitialized) {
		t.Fatalf("err = %v, want ErrMilvusNotInitialized", err)
	}
}

// TestProductVectorAgainstRealMilvus 需要真实 Milvus，-short 跳过
func TestProductVectorAgainstRealMilvus(t *testing.T) {
	if testing.Short() {
		t.Skip("skip: requires real Milvus")
	}
	t.Skip("integration test placeholder; set MILVUS_ADDR and remove this skip to run")
}
