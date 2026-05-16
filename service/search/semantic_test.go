package search

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/RedInn7/gomall/repository/db/model"
	"github.com/RedInn7/gomall/repository/es"
	"github.com/RedInn7/gomall/types"
)

// fakeSearcher 单测用 Milvus 替身，按预置 Hit 列表回放
type fakeSearcher struct {
	hits []Hit
}

func (f fakeSearcher) Search(_ context.Context, _ []float32, _ int, _ *uint) ([]Hit, error) {
	return f.hits, nil
}

func newProduct(id uint, name string) *model.Product {
	p := &model.Product{
		Name:  name,
		Title: name,
	}
	p.ID = id
	p.CreatedAt = time.Unix(1700000000, 0)
	return p
}

func TestSemanticSearchHybridFusionAndTopK(t *testing.T) {
	prev := GetSearcher()
	defer SetSearcher(prev)

	// 向量召回: id=1 分最高, id=2 中, id=3 低
	SetSearcher(fakeSearcher{hits: []Hit{
		{ID: 1, Score: 0.9},
		{ID: 2, Score: 0.5},
		{ID: 3, Score: 0.1},
	}})

	deps := hybridDeps{
		embed: func(_ context.Context, _ string) ([]float32, error) {
			return []float32{0.1, 0.2, 0.3}, nil
		},
		keyword: func(_ context.Context, _ string, _, _ int, _ *uint) ([]es.ScoredProductDoc, int64, error) {
			// 关键词召回: id=3 分最高, id=2 中, id=4 (向量未命中) 低
			return []es.ScoredProductDoc{
				{Doc: &es.ProductDoc{ID: 3}, Score: 10},
				{Doc: &es.ProductDoc{ID: 2}, Score: 5},
				{Doc: &es.ProductDoc{ID: 4}, Score: 1},
			}, 3, nil
		},
		loader: func(_ context.Context, ids []uint) ([]*model.Product, error) {
			out := make([]*model.Product, 0, len(ids))
			for _, id := range ids {
				out = append(out, newProduct(id, "p"))
			}
			return out, nil
		},
	}

	hits, err := semanticSearchWith(context.Background(), &types.ProductSemanticSearchReq{
		Query: "笔记本",
		TopK:  3,
	}, deps)
	if err != nil {
		t.Fatalf("semanticSearchWith err=%v", err)
	}

	if len(hits) != 3 {
		t.Fatalf("expected topK=3 hits, got %d", len(hits))
	}

	// 期望融合分数:
	// id=1: semNorm=1, kwNorm=0    -> 0.5
	// id=2: semNorm=0.5, kwNorm=0.444... -> ~0.472
	// id=3: semNorm=0, kwNorm=1    -> 0.5
	// id=4: semNorm 缺, kwNorm=0    -> 0
	// 排序: id=1 (0.5, 较小id优先), id=3 (0.5), id=2, 截断后 id=4 不出现
	want := []uint{1, 3, 2}
	for i, h := range hits {
		if h.Product.ID != want[i] {
			t.Errorf("position %d: want id=%d got id=%d (score=%f)", i, want[i], h.Product.ID, h.Score)
		}
	}

	// 校验融合分数确实是 50/50 加权
	for _, h := range hits {
		expected := weightSemantic*h.SemanticScore + weightKeyword*h.KeywordScore
		if math.Abs(float64(expected-h.Score)) > 1e-6 {
			t.Errorf("id=%d fused score mismatch: want %f got %f", h.Product.ID, expected, h.Score)
		}
	}
}

func TestSemanticSearchDefaultsAndValidation(t *testing.T) {
	prev := GetSearcher()
	defer SetSearcher(prev)
	SetSearcher(fakeSearcher{hits: nil})

	deps := hybridDeps{
		embed: func(_ context.Context, _ string) ([]float32, error) {
			return []float32{0.1}, nil
		},
		keyword: func(_ context.Context, _ string, _, size int, _ *uint) ([]es.ScoredProductDoc, int64, error) {
			// 校验 TopK 默认 10 -> 关键词召回 size = 30
			if size != defaultTopK*3 {
				t.Errorf("keyword size = %d, want %d", size, defaultTopK*3)
			}
			return nil, 0, nil
		},
		loader: func(_ context.Context, ids []uint) ([]*model.Product, error) {
			if len(ids) != 0 {
				t.Errorf("loader called with %d ids, want 0", len(ids))
			}
			return nil, nil
		},
	}

	if _, err := semanticSearchWith(context.Background(), &types.ProductSemanticSearchReq{Query: "x"}, deps); err != nil {
		t.Fatalf("err=%v", err)
	}

	if _, err := semanticSearchWith(context.Background(), &types.ProductSemanticSearchReq{Query: ""}, deps); err == nil {
		t.Fatal("expected error for empty query")
	}
}

func TestMinMaxNormalize(t *testing.T) {
	got := minMaxNormalize([]float32{1, 3, 5})
	want := []float32{0, 0.5, 1}
	for i := range got {
		if math.Abs(float64(got[i]-want[i])) > 1e-6 {
			t.Errorf("idx %d: got %f want %f", i, got[i], want[i])
		}
	}

	// 同值: 全部 1
	got = minMaxNormalize([]float32{2, 2, 2})
	for _, v := range got {
		if v != 1 {
			t.Errorf("flat normalize want 1 got %f", v)
		}
	}

	if minMaxNormalize(nil) != nil {
		t.Error("nil input should yield nil")
	}
}

func TestEmbedTextStubDeterministic(t *testing.T) {
	a, err := EmbedText(context.Background(), "hello")
	if err != nil {
		t.Fatalf("EmbedText err=%v", err)
	}
	b, _ := EmbedText(context.Background(), "hello")
	if len(a) != embeddingDim {
		t.Fatalf("dim=%d want %d", len(a), embeddingDim)
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("stub embedding not deterministic at %d: %f vs %f", i, a[i], b[i])
		}
	}
}
