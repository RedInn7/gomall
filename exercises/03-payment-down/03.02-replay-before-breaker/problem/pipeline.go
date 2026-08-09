//go:build exercise

package replaybreaker

import "errors"

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

func NewCache() *Cache                     { return &Cache{done: map[string]string{}} }
func (c *Cache) Seed(key, response string) { c.done[key] = response }

func Handle(cache *Cache, breaker *Breaker, key string, pay func() (string, error)) (string, error) {
	// TODO: 幂等回放必须发生在熔断判断之前；只缓存成功，并只统计系统失败。
	return "", nil
}
