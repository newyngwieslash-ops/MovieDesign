package agent

import (
	"errors"
	"sync"
)

var ErrBudget = errors.New("agent tool budget exhausted")

type Budget struct {
	mu    sync.Mutex
	limit int
	calls map[string]error
	count int
}

func NewBudget(limit int) *Budget { return &Budget{limit: limit, calls: map[string]error{}} }
func (b *Budget) Admit(id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if id == "" {
		return errors.New("call identity required")
	}
	if e, ok := b.calls[id]; ok {
		return e
	}
	var e error
	if b.count >= b.limit {
		e = ErrBudget
	} else {
		b.count++
	}
	b.calls[id] = e
	return e
}
