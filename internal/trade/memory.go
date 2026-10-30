package trade

import (
	"context"
	"sync"
)

// MemoryStore is an in-process Store for development and tests.
type MemoryStore struct {
	mu     sync.Mutex
	trades map[string]*Trade
}

// NewMemoryStore returns an empty store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{trades: make(map[string]*Trade)}
}

func (m *MemoryStore) Create(_ context.Context, t *Trade) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *t
	m.trades[t.ID] = &cp
	return nil
}

func (m *MemoryStore) Get(_ context.Context, id string) (*Trade, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.trades[id]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *t
	return &cp, nil
}

func (m *MemoryStore) Transition(_ context.Context, id string, from, to State, version uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.trades[id]
	if !ok {
		return ErrNotFound
	}
	if t.State != from || t.Version+1 != version {
		return ErrConflict
	}
	t.State = to
	t.Version = version
	return nil
}

func (m *MemoryStore) ListPending(_ context.Context, limit int64) ([]Trade, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 {
		limit = 100
	}
	var out []Trade
	for _, t := range m.trades {
		switch t.State {
		case ALocked, BothLocked, Settling, Refunding:
			out = append(out, *t)
			if int64(len(out)) >= limit {
				return out, nil
			}
		}
	}
	return out, nil
}
