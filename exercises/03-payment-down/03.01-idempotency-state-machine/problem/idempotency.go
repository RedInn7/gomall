//go:build exercise

package idempotencysm

import "errors"

var (
	ErrEmptyKey      = errors.New("empty idempotency key")
	ErrNotProcessing = errors.New("request is not processing")
	ErrAlreadyDone   = errors.New("request already done")
)

type Action string

const (
	Acquired Action = "ACQUIRED"
	Wait     Action = "WAIT"
	Replay   Action = "REPLAY"
)

type record struct{ state, response string }
type Store struct{ records map[string]record }

func NewStore() *Store { return &Store{records: map[string]record{}} }

func (s *Store) Begin(key string) (Action, string, error) {
	// TODO: 开始或重放一次幂等支付请求。
	// 1. 空 key 返回 ErrEmptyKey；
	// 2. key 不存在时创建 processing 记录，返回 Acquired 和空响应；
	// 3. key 已处于 processing 时返回 Wait 和空响应；
	// 4. key 已处于 done 时返回 Replay 和之前保存的响应。
	return "", "", nil
}

func (s *Store) CompleteSuccess(key, response string) error {
	// TODO: 只允许 processing 记录进入 done，并保存 response。
	// key 不存在或状态不是 processing 时返回 ErrNotProcessing；已经 done 时返回 ErrAlreadyDone，
	// 且不能覆盖第一次保存的成功响应。
	return nil
}
