package search

import (
	"context"
	"reflect"
	"testing"

	"github.com/RedInn7/gomall/internal/product"
	"github.com/RedInn7/gomall/service/events"
)

type recordingVectorStore struct {
	searchCalls int
	upserts     []vectorUpsert
	deletes     []uint
}

type vectorUpsert struct {
	id         uint
	categoryID uint
	vector     []float32
}

func (s *recordingVectorStore) Search(context.Context, []float32, int, *uint) ([]Hit, error) {
	s.searchCalls++
	return []Hit{{ID: 42, Score: 0.2}}, nil
}

func (s *recordingVectorStore) Upsert(_ context.Context, id uint, vec []float32, categoryID uint) error {
	s.upserts = append(s.upserts, vectorUpsert{id: id, categoryID: categoryID, vector: vec})
	return nil
}

func (s *recordingVectorStore) Delete(_ context.Context, id uint) error {
	s.deletes = append(s.deletes, id)
	return nil
}

func installVectorStoreForTest(t *testing.T, store ProductVectorStore) {
	t.Helper()
	SetProductVectorStore(store)
	t.Cleanup(func() { SetProductVectorStore(nil) })
}

func TestSetProductVectorStoreConnectsQueryPath(t *testing.T) {
	store := &recordingVectorStore{}
	installVectorStoreForTest(t, store)

	hits, err := GetSearcher().Search(context.Background(), make([]float32, embeddingDim), 3, nil)
	if err != nil {
		t.Fatal(err)
	}
	if store.searchCalls != 1 || len(hits) != 1 || hits[0].ID != 42 {
		t.Fatalf("Milvus query adapter was not used: calls=%d hits=%#v", store.searchCalls, hits)
	}
}

func TestProductChangedUpdatesKeywordAndVectorIndexes(t *testing.T) {
	store := &recordingVectorStore{}
	installVectorStoreForTest(t, store)
	p := &product.Product{Name: "Phone", Title: "Flagship", Info: "great camera", CategoryID: 7, OnSale: true}
	p.ID = 12
	var keywordID uint
	var embeddedText string

	err := handleKeywordProductChangedWith(context.Background(), events.ProductChanged{ProductID: 12, Op: "update"},
		func(uint) (*product.Product, error) { return p, nil },
		func(_ context.Context, got *product.Product) error { keywordID = got.ID; return nil },
		func(context.Context, uint) error { return nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	err = handleVectorProductChangedWith(context.Background(), events.ProductChanged{ProductID: 12, Op: "update"},
		func(uint) (*product.Product, error) { return p, nil },
		func(_ context.Context, text string) ([]float32, error) {
			embeddedText = text
			return []float32{1, 2, 3}, nil
		},
		store,
	)
	if err != nil {
		t.Fatal(err)
	}
	if keywordID != 12 || embeddedText != "Phone\nFlagship\ngreat camera" {
		t.Fatalf("unexpected indexing input: keyword=%d text=%q", keywordID, embeddedText)
	}
	want := []vectorUpsert{{id: 12, categoryID: 7, vector: []float32{1, 2, 3}}}
	if !reflect.DeepEqual(store.upserts, want) {
		t.Fatalf("vector upsert mismatch: got %#v want %#v", store.upserts, want)
	}
}

func TestProductDeletedRemovesBothIndexes(t *testing.T) {
	store := &recordingVectorStore{}
	installVectorStoreForTest(t, store)
	var keywordDeletes []uint

	err := handleKeywordProductChangedWith(context.Background(), events.ProductChanged{ProductID: 19, Op: "delete"}, nil, nil,
		func(_ context.Context, id uint) error { keywordDeletes = append(keywordDeletes, id); return nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	err = handleVectorProductChangedWith(context.Background(), events.ProductChanged{ProductID: 19, Op: "delete"}, nil, nil, store)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(keywordDeletes, []uint{19}) || !reflect.DeepEqual(store.deletes, []uint{19}) {
		t.Fatalf("delete did not reach both indexes: keyword=%v vector=%v", keywordDeletes, store.deletes)
	}
}

func TestOffSaleProductIsRemovedFromVectorIndex(t *testing.T) {
	store := &recordingVectorStore{}
	p := &product.Product{Name: "hidden", OnSale: false}
	p.ID = 23
	embedCalled := false
	err := handleVectorProductChangedWith(context.Background(), events.ProductChanged{ProductID: 23, Op: "update"},
		func(uint) (*product.Product, error) { return p, nil },
		func(context.Context, string) ([]float32, error) { embedCalled = true; return nil, nil },
		store,
	)
	if err != nil {
		t.Fatal(err)
	}
	if embedCalled || len(store.upserts) != 0 || !reflect.DeepEqual(store.deletes, []uint{23}) {
		t.Fatalf("off-sale product was not removed: embed=%v upserts=%v deletes=%v", embedCalled, store.upserts, store.deletes)
	}
}
