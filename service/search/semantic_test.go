package search

import (
	"context"
	"testing"

	"github.com/RedInn7/gomall/internal/product"
	"github.com/RedInn7/gomall/repository/es"
)

type fixedVectorSearcher struct{ hits []Hit }

func (s fixedVectorSearcher) Search(context.Context, []float32, int, *uint) ([]Hit, error) {
	return s.hits, nil
}

func TestSemanticSearchRanksSmallerL2DistanceFirst(t *testing.T) {
	previous := GetSearcher()
	SetSearcher(fixedVectorSearcher{hits: []Hit{{ID: 1, Score: 0.1}, {ID: 2, Score: 0.9}}})
	t.Cleanup(func() { SetSearcher(previous) })

	products := map[uint]*product.Product{
		1: {Name: "closest"},
		2: {Name: "farthest"},
	}
	products[1].ID = 1
	products[2].ID = 2

	hits, err := semanticSearchWith(context.Background(), &product.ProductSemanticSearchReq{Query: "phone", TopK: 2}, hybridDeps{
		embed: func(context.Context, string) ([]float32, error) { return make([]float32, embeddingDim), nil },
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
