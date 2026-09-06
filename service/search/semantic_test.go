package search

import (
	"context"
	"errors"
	"testing"

	"github.com/RedInn7/gomall/internal/product"
	"github.com/RedInn7/gomall/repository/es"
)

type fixedVectorSearcher struct{ hits []Hit }

func (s fixedVectorSearcher) Search(context.Context, []float32, int, *uint) ([]Hit, error) {
	return s.hits, nil
}

func TestSemanticSearchFallsBackToKeywordWhenEmbeddingFails(t *testing.T) {
	productRow := &product.Product{Name: "keyword result", OnSale: true}
	productRow.ID = 8
	hits, err := semanticSearchWith(context.Background(), &product.ProductSemanticSearchReq{Query: "phone", TopK: 2}, hybridDeps{
		embed:  func(context.Context, string) ([]float32, error) { return nil, errors.New("embedding unavailable") },
		vector: fixedVectorSearcher{},
		keyword: func(context.Context, string, int, int, *uint) ([]es.ScoredProductDoc, int64, error) {
			return []es.ScoredProductDoc{{Doc: &es.ProductDoc{ID: 8}, Score: 3}}, 1, nil
		},
		loader: func(context.Context, []uint) ([]*product.Product, error) {
			return []*product.Product{productRow}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Product.ID != 8 || hits[0].KeywordScore != 1 {
		t.Fatalf("keyword fallback missing: %#v", hits)
	}
}

func TestSemanticSearchRanksSmallerL2DistanceFirst(t *testing.T) {
	products := map[uint]*product.Product{
		1: {Name: "closest", OnSale: true},
		2: {Name: "farthest", OnSale: true},
	}
	products[1].ID = 1
	products[2].ID = 2

	hits, err := semanticSearchWith(context.Background(), &product.ProductSemanticSearchReq{Query: "phone", TopK: 2}, hybridDeps{
		embed:  func(context.Context, string) ([]float32, error) { return make([]float32, embeddingDim), nil },
		vector: fixedVectorSearcher{hits: []Hit{{ID: 1, Score: 0.1}, {ID: 2, Score: 0.9}}},
		keyword: func(context.Context, string, int, int, *uint) ([]es.ScoredProductDoc, int64, error) {
			return nil, 0, nil
		},
		loader: func(_ context.Context, ids []uint) ([]*product.Product, error) {
			out := make([]*product.Product, 0, len(ids))
			for _, id := range ids {
				out = append(out, products[id])
			}
			return out, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 || hits[0].Product.ID != 1 {
		t.Fatalf("nearest vector must rank first, got %#v", hits)
	}
}

func TestSemanticSearchRejectsWhitespaceOnlyQuery(t *testing.T) {
	called := false
	_, err := semanticSearchWith(context.Background(), &product.ProductSemanticSearchReq{Query: " \t\n "}, hybridDeps{
		embed:  func(context.Context, string) ([]float32, error) { called = true; return nil, nil },
		vector: fixedVectorSearcher{},
		keyword: func(context.Context, string, int, int, *uint) ([]es.ScoredProductDoc, int64, error) {
			called = true
			return nil, 0, nil
		},
		loader: func(context.Context, []uint) ([]*product.Product, error) { return nil, nil },
	})
	if err == nil || called {
		t.Fatalf("whitespace query must fail before calling dependencies: err=%v called=%v", err, called)
	}
}

func TestSemanticSearchFiltersProductsThatAreNotOnSale(t *testing.T) {
	visible := &product.Product{Name: "visible", OnSale: true}
	visible.ID = 1
	hidden := &product.Product{Name: "hidden", OnSale: false}
	hidden.ID = 2

	hits, err := semanticSearchWith(context.Background(), &product.ProductSemanticSearchReq{Query: "phone", TopK: 2}, hybridDeps{
		embed:  func(context.Context, string) ([]float32, error) { return make([]float32, embeddingDim), nil },
		vector: fixedVectorSearcher{hits: []Hit{{ID: 1, Score: 0.1}, {ID: 2, Score: 0.2}}},
		keyword: func(context.Context, string, int, int, *uint) ([]es.ScoredProductDoc, int64, error) {
			return nil, 0, nil
		},
		loader: func(context.Context, []uint) ([]*product.Product, error) {
			return []*product.Product{visible, hidden}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Product.ID != visible.ID {
		t.Fatalf("off-sale product leaked into search results: %#v", hits)
	}
}
