package trade

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

var (
	// ErrWrongState means the trade is not in a state that allows the call.
	ErrWrongState = errors.New("trade wrong state")
	// ErrExpired means the trade passed its expiry.
	ErrExpired = errors.New("trade expired")
)

// Store is the persistence contract used by Service.
type Store interface {
	Create(ctx context.Context, t *Trade) error
	Get(ctx context.Context, id string) (*Trade, error)
	Transition(ctx context.Context, id string, from, to State, version uint64) error
	ListPending(ctx context.Context, limit int64) ([]Trade, error)
}

// Escrow moves assets on a player aggregate.
type Escrow interface {
	Lock(ctx context.Context, playerID uint64, tradeID string, a Assets) error
	Refund(ctx context.Context, playerID uint64, tradeID string) error
	Settle(ctx context.Context, playerID uint64, tradeID string, credit Assets) error
}

// Service coordinates the directed-trade state machine. It is stateless;
// retries are safe because every step is idempotent.
type Service struct {
	store  Store
	escrow Escrow
	now    func() time.Time
	newID  func() string
}

// NewService builds a coordinator.
func NewService(store Store, escrow Escrow) *Service {
	return &Service{
		store:  store,
		escrow: escrow,
		now:    time.Now,
		newID:  randomID,
	}
}

// Create locks A's assets and writes the trade document.
func (s *Service) Create(ctx context.Context, world, season uint32, a, b uint64, aAssets, bAssets Assets, ttl time.Duration) (*Trade, error) {
	id := s.newID()
	if err := s.escrow.Lock(ctx, a, id, aAssets); err != nil {
		return nil, err
	}
	t := &Trade{
		ID:       id,
		World:    world,
		Season:   season,
		A:        a,
		B:        b,
		AAssets:  aAssets,
		BAssets:  bAssets,
		State:    ALocked,
		Version:  1,
		ExpireAt: s.now().Add(ttl).UnixMilli(),
	}
	if err := s.store.Create(ctx, t); err != nil {
		_ = s.escrow.Refund(ctx, a, id)
		return nil, err
	}
	return t, nil
}

// Accept locks B's assets and moves the trade to both_locked.
func (s *Service) Accept(ctx context.Context, tradeID string, b uint64) error {
	t, err := s.store.Get(ctx, tradeID)
	if err != nil {
		return err
	}
	if t.State != ALocked || t.B != b {
		return ErrWrongState
	}
	if err := s.escrow.Lock(ctx, b, tradeID, t.BAssets); err != nil {
		return err
	}
	if err := s.store.Transition(ctx, tradeID, ALocked, BothLocked, t.Version+1); err != nil {
		_ = s.escrow.Refund(ctx, b, tradeID)
		return err
	}
	return nil
}

// Settle credits both sides. It is resumable from the settling state.
func (s *Service) Settle(ctx context.Context, tradeID string) error {
	t, err := s.store.Get(ctx, tradeID)
	if err != nil {
		return err
	}
	if t.State == Settled {
		return nil
	}
	if t.State == BothLocked {
		if err := s.store.Transition(ctx, tradeID, BothLocked, Settling, t.Version+1); err != nil {
			return err
		}
		t.State = Settling
		t.Version++
	}
	if t.State != Settling {
		return ErrWrongState
	}
	if err := s.escrow.Settle(ctx, t.A, tradeID, t.BAssets); err != nil {
		return fmt.Errorf("settle A: %w", err)
	}
	if err := s.escrow.Settle(ctx, t.B, tradeID, t.AAssets); err != nil {
		return fmt.Errorf("settle B: %w", err)
	}
	return s.store.Transition(ctx, tradeID, Settling, Settled, t.Version+1)
}

// Cancel refunds both sides. It is resumable from the refunding state.
func (s *Service) Cancel(ctx context.Context, tradeID string) error {
	t, err := s.store.Get(ctx, tradeID)
	if err != nil {
		return err
	}
	switch t.State {
	case Refunded:
		return nil
	case ALocked, BothLocked:
		if err := s.store.Transition(ctx, tradeID, t.State, Refunding, t.Version+1); err != nil {
			return err
		}
		t.State = Refunding
		t.Version++
	case Refunding:
	default:
		return ErrWrongState
	}
	if err := s.escrow.Refund(ctx, t.A, tradeID); err != nil {
		return fmt.Errorf("refund A: %w", err)
	}
	if err := s.escrow.Refund(ctx, t.B, tradeID); err != nil {
		return fmt.Errorf("refund B: %w", err)
	}
	return s.store.Transition(ctx, tradeID, Refunding, Refunded, t.Version+1)
}

// Reconcile resumes non-terminal trades: settling ones settle, expired ones
// refund. It returns the number of trades it touched.
func (s *Service) Reconcile(ctx context.Context, limit int64) (int, error) {
	pending, err := s.store.ListPending(ctx, limit)
	if err != nil {
		return 0, err
	}
	touched := 0
	for i := range pending {
		t := &pending[i]
		if t.State == Settling {
			if err := s.Settle(ctx, t.ID); err != nil {
				continue
			}
			touched++
			continue
		}
		if s.now().UnixMilli() >= t.ExpireAt {
			if err := s.Cancel(ctx, t.ID); err != nil {
				continue
			}
			touched++
		}
	}
	return touched, nil
}

// Get loads one trade.
func (s *Service) Get(ctx context.Context, id string) (*Trade, error) {
	return s.store.Get(ctx, id)
}

func randomID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("t-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}
