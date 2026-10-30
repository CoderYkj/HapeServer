// Package battle implements the P0 submit-and-settle battle model.
package battle

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// State is the battle lifecycle.
type State string

const (
	Created State = "created"
	Settled State = "settled"
)

// ErrNotFound means the battle does not exist.
var ErrNotFound = errors.New("battle not found")

// Battle is one resolved combat instance.
type Battle struct {
	ID            string `json:"_id" bson:"_id"`
	World         uint32 `json:"world"`
	Season        uint32 `json:"season"`
	Attacker      uint64 `json:"attacker"`
	Defender      uint64 `json:"defender"`
	AttackerPower int64  `json:"attackerPower"`
	DefenderPower int64  `json:"defenderPower"`
	Winner        uint64 `json:"winner"`
	State         State  `json:"state"`
	CreatedAt     int64  `json:"createdAt"`
	SettledAt     int64  `json:"settledAt"`
}

// Resolve picks the winner: higher power wins, ties go to the defender.
func Resolve(attacker, defender uint64, ap, dp int64) uint64 {
	if ap > dp {
		return attacker
	}
	return defender
}

// Store persists battles.
type Store interface {
	Create(ctx context.Context, b *Battle) error
	Get(ctx context.Context, id string) (*Battle, error)
	ListByPlayer(ctx context.Context, playerID uint64, season uint32, before int64, beforeID string, limit int64) ([]Battle, error)
}

// Memory is an in-memory store for P0.
type Memory struct {
	mu sync.Mutex
	m  map[string]*Battle
}

// NewMemory builds an empty store.
func NewMemory() *Memory { return &Memory{m: make(map[string]*Battle)} }

func (s *Memory) Create(_ context.Context, b *Battle) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[b.ID] = b
	return nil
}

func (s *Memory) Get(_ context.Context, id string) (*Battle, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.m[id]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *b
	return &cp, nil
}

// Service creates and stores battle instances.
type Service struct {
	store Store
	mu    sync.Mutex
	seq   uint64
}

// NewService wires a battle service.
func NewService(store Store) *Service {
	if store == nil {
		store = NewMemory()
	}
	return &Service{store: store}
}

// Submit resolves a battle and stores the result.
func (s *Service) Submit(ctx context.Context, world, season uint32, attacker, defender uint64, ap, dp int64) (*Battle, error) {
	if attacker == 0 || defender == 0 {
		return nil, errors.New("battle: player id required")
	}
	s.mu.Lock()
	s.seq++
	seq := s.seq
	s.mu.Unlock()
	now := time.Now().UnixMilli()
	b := &Battle{
		ID:            fmt.Sprintf("b-%d-%d", now, seq),
		World:         world,
		Season:        season,
		Attacker:      attacker,
		Defender:      defender,
		AttackerPower: ap,
		DefenderPower: dp,
		Winner:        Resolve(attacker, defender, ap, dp),
		State:         Settled,
		CreatedAt:     now,
		SettledAt:     now,
	}
	if err := s.store.Create(ctx, b); err != nil {
		return nil, err
	}
	return b, nil
}

// Get loads one battle.
func (s *Service) Get(ctx context.Context, id string) (*Battle, error) {
	return s.store.Get(ctx, id)
}

// Page is one page of battle history.
type Page struct {
	Items         []Battle `json:"items"`
	NextCreatedAt int64    `json:"nextCreatedAt,omitempty"`
	NextID        string   `json:"nextId,omitempty"`
}

// History returns a player's battle history, newest first, using
// (createdAt, id) as a compound cursor.
func (s *Service) History(ctx context.Context, playerID uint64, season uint32, before int64, beforeID string, limit int64) (*Page, error) {
	items, err := s.store.ListByPlayer(ctx, playerID, season, before, beforeID, limit)
	if err != nil {
		return nil, err
	}
	page := &Page{Items: items}
	if int64(len(items)) == limit && len(items) > 0 {
		last := items[len(items)-1]
		page.NextCreatedAt = last.CreatedAt
		page.NextID = last.ID
	}
	return page, nil
}

// ListByPlayer returns a player's battles, newest first.
func (s *Memory) ListByPlayer(_ context.Context, playerID uint64, season uint32, before int64, beforeID string, limit int64) ([]Battle, error) {
	if limit <= 0 {
		limit = 20
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Battle, 0)
	for _, b := range s.m {
		if b.Attacker != playerID && b.Defender != playerID {
			continue
		}
		if season != 0 && b.Season != season {
			continue
		}
		if before > 0 {
			if b.CreatedAt > before || (beforeID != "" && b.CreatedAt == before && b.ID >= beforeID) {
				continue
			}
		}
		out = append(out, *b)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt > out[j].CreatedAt
		}
		return out[i].ID > out[j].ID
	})
	if int64(len(out)) > limit {
		out = out[:limit]
	}
	return out, nil
}
