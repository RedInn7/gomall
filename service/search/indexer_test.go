package search

import (
	"context"
	"errors"
	"io"
	"reflect"
	"testing"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"

	"github.com/RedInn7/gomall/internal/product"
	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/service/events"
)

func TestMissingProductIsDeletedFromBothIndexes(t *testing.T) {
	ctx := context.Background()
	ev := events.ProductChanged{ProductID: 31, Op: "update"}
	notFound := func(uint) (*product.Product, error) { return nil, gorm.ErrRecordNotFound }
	var keywordDeletes []uint

	err := handleKeywordProductChangedWith(ctx, ev, notFound,
		func(context.Context, *product.Product) error {
			t.Fatal("missing product must not be upserted")
			return nil
		},
		func(_ context.Context, id uint) error { keywordDeletes = append(keywordDeletes, id); return nil },
	)
	if err != nil {
		t.Fatalf("missing product must not be retried into DLQ: %v", err)
	}
	store := &recordingVectorStore{}
	if err := handleVectorProductChangedWith(ctx, ev, notFound, nil, store); err != nil {
		t.Fatalf("missing product must not be retried into DLQ: %v", err)
	}
	if !reflect.DeepEqual(keywordDeletes, []uint{31}) || !reflect.DeepEqual(store.deletes, []uint{31}) {
		t.Fatalf("missing product not removed: keyword=%v vector=%v", keywordDeletes, store.deletes)
	}
}

func TestBackfillRecordsFailedProductAndKeepsGoing(t *testing.T) {
	logger := logrus.New()
	logger.Out = io.Discard
	previous := util.LogrusObj
	util.LogrusObj = logger
	t.Cleanup(func() { util.LogrusObj = previous })

	var rows []*product.Product
	for id := uint(1); id <= 3; id++ {
		p := &product.Product{}
		p.ID = id
		rows = append(rows, p)
	}
	listCalls := 0
	list := func(lastID uint, limit int) ([]*product.Product, error) {
		if listCalls++; listCalls > 10 {
			t.Fatalf("backfill keeps re-reading the same batch after lastID=%d", lastID)
		}
		var out []*product.Product
		for _, p := range rows {
			if p.ID > lastID && len(out) < limit {
				out = append(out, p)
			}
		}
		return out, nil
	}
	// 每批最后一条失败：旧实现游标停在它之前，会一直拉同一批空转
	index := func(_ context.Context, p *product.Product) error {
		if p.ID == 3 {
			return errors.New("es 500")
		}
		return nil
	}

	indexed, failed, err := backfill(context.Background(), 2, list, index)
	if err != nil || indexed != 2 || !reflect.DeepEqual(failed, []uint{3}) {
		t.Fatalf("got indexed=%d failed=%v err=%v", indexed, failed, err)
	}
}
