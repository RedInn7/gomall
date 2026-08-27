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

func SaveProductAndEvent(db *DB, product Product, eventID string) error {
	if product.ID == 0 {
		return ErrInvalidProductID
	}
	eventID = strings.TrimSpace(eventID)
	if eventID == "" {
		return ErrEmptyEventID
	}

	return db.Transaction(func(tx *Tx) error {
		if existing, ok := tx.events[eventID]; ok {
			if existing.ProductID == product.ID && existing.Version == product.Version {
				return nil
			}
			return ErrEventConflict
		}

		if existing, ok := tx.products[product.ID]; ok {
			if product.Version < existing.Version {
				return ErrStaleVersion
			}
			if product.Version == existing.Version && product != existing {
				return ErrVersionConflict
			}
		}

		tx.products[product.ID] = product
		return tx.insertEvent(Event{ID: eventID, Topic: "product.changed", ProductID: product.ID, Version: product.Version})
	})
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
