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
	// TODO: 按固定顺序处理支付：
	// 1. cache 中已有 key 时直接回放响应，不调用 pay，也不受熔断状态影响；
	// 2. 没有缓存且 breaker.Open 时返回 ErrCircuitOpen，不调用 pay；
	// 3. 调用 pay。成功时缓存并返回响应；失败时原样返回错误，不得缓存；
	// 4. 只有错误是 Kind == SystemError 的 *PaymentError 时，Failures 才加一。
	return "", nil
}
