//go:build exercise

package indexoutbox

import (
	"errors"
	"strings"
)

var (
	ErrInvalidProductID  = errors.New("invalid product id")
	ErrEmptyEventID      = errors.New("empty event id")
	ErrOutboxUnavailable = errors.New("outbox unavailable")
	ErrEventConflict     = errors.New("event id conflict")
	ErrStaleVersion      = errors.New("stale product version")
	ErrVersionConflict   = errors.New("product version conflict")
)

type Product struct {
	ID      uint
	Name    string
	Version uint64
}

type Event struct {
	ID        string
	Topic     string
	ProductID uint
	Version   uint64
}

type DB struct {
	Products   map[uint]Product
	Events     map[string]Event
	FailOutbox bool
}

type Tx struct {
	products   map[uint]Product
	events     map[string]Event
	failOutbox bool
}

func NewDB() *DB {
	return &DB{Products: map[uint]Product{}, Events: map[string]Event{}}
}

func (db *DB) Transaction(fn func(*Tx) error) error {
	tx := &Tx{products: cloneProducts(db.Products), events: cloneEvents(db.Events), failOutbox: db.FailOutbox}
	if err := fn(tx); err != nil {
		return err
	}
	db.Products = tx.products
	db.Events = tx.events
	return nil
}

func (tx *Tx) insertEvent(event Event) error {
	if tx.failOutbox {
		return ErrOutboxUnavailable
	}
	tx.events[event.ID] = event
	return nil
}

// SaveProductAndEvent atomically stores the source-of-truth product and the
// event that will later refresh its Elasticsearch document.
func SaveProductAndEvent(db *DB, product Product, eventID string) error {
	// TODO: 完成商品与索引事件的原子写入：
	// 1. product.ID 为 0 时返回 ErrInvalidProductID；eventID 去除首尾空白后
	//    为空时返回 ErrEmptyEventID。校验失败不得改变 DB。
	// 2. 在 db.Transaction 内完成下面所有判断和写入。
	// 3. eventID 已存在且 ProductID、Version 与本次一致时按幂等成功处理，
	//    不覆盖已有商品；若二者任一不一致，返回 ErrEventConflict。
	// 4. 对已有商品：较小 Version 返回 ErrStaleVersion；相同 Version 但内容
	//    不同返回 ErrVersionConflict；相同内容允许继续记录新的事件。
	// 5. 写入 product 后，调用 tx.insertEvent 写入 Topic 为 "product.changed"、
	//    ProductID 和 Version 与商品一致的事件，并原样返回错误。
	// 6. 任意错误都必须依靠事务保留调用前的 Products 与 Events。
	_ = strings.TrimSpace(eventID)
	return nil
}

func cloneProducts(src map[uint]Product) map[uint]Product {
	dst := make(map[uint]Product, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func cloneEvents(src map[string]Event) map[string]Event {
	dst := make(map[string]Event, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}
