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
	// TODO: 按 init / processing / done 状态返回 ACQUIRED / WAIT / REPLAY。
	return "", "", nil
}

func (s *Store) CompleteSuccess(key, response string) error {
	// TODO: 只允许 processing -> done，并保存成功响应。
	return nil
}
