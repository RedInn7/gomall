//go:build exercise

package replaybreaker

import (
	"errors"
)

var ErrCircuitOpen = errors.New("circuit open")

type ErrorKind string

const (
	BusinessError ErrorKind = "business"
	SystemError   ErrorKind = "system"
)

type PaymentError struct {
	Kind    ErrorKind
	Message string
}

func (e *PaymentError) Error() string { return e.Message }

type Breaker struct {
	Open     bool
	Failures int
}
type Cache struct{ done map[string]string }

func NewCache() *Cache            { return &Cache{done: map[string]string{}} }
func (c *Cache) Seed(k, r string) { c.done[k] = r }
func Handle(cache *Cache, breaker *Breaker, key string, pay func() (string, error)) (string, error) {
	if response, ok := cache.done[key]; ok {
		return response, nil
	}
	if breaker.Open {
		return "", ErrCircuitOpen
	}
	response, err := pay()
	if err != nil {
		var pe *PaymentError
		if errors.As(err, &pe) && pe.Kind == SystemError {
			breaker.Failures++
		}
		return "", err
	}
	cache.done[key] = response
	return response, nil
}
