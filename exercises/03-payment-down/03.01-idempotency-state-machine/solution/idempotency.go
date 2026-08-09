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
	if key == "" {
		return "", "", ErrEmptyKey
	}
	r, ok := s.records[key]
	if !ok {
		s.records[key] = record{state: "processing"}
		return Acquired, "", nil
	}
	if r.state == "done" {
		return Replay, r.response, nil
	}
	return Wait, "", nil
}
func (s *Store) CompleteSuccess(key, response string) error {
	r, ok := s.records[key]
	if !ok {
		return ErrNotProcessing
	}
	if r.state == "done" {
		return ErrAlreadyDone
	}
	if r.state != "processing" {
		return ErrNotProcessing
	}
	s.records[key] = record{state: "done", response: response}
	return nil
}
