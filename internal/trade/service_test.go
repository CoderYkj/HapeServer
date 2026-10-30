package trade

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

type memStore struct {
	mu             sync.Mutex
	trades         map[string]*Trade
	failCreate     bool
	failTransition bool
}

func newMemStore() *memStore { return &memStore{trades: make(map[string]*Trade)} }

func (m *memStore) Create(_ context.Context, t *Trade) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failCreate {
		return errors.New("create failed")
	}
	cp := *t
	m.trades[t.ID] = &cp
	return nil
}

func (m *memStore) Get(_ context.Context, id string) (*Trade, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.trades[id]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *t
	return &cp, nil
}

func (m *memStore) Transition(_ context.Context, id string, from, to State, version uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failTransition {
		return errors.New("transition failed")
	}
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

func (m *memStore) ListPending(_ context.Context, _ int64) ([]Trade, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Trade
	for _, t := range m.trades {
		switch t.State {
		case ALocked, BothLocked, Settling, Refunding:
			out = append(out, *t)
		}
	}
	return out, nil
}

type memEscrow struct {
	mu      sync.Mutex
	balance map[uint64]int64
	escrow  map[string]map[uint64]Assets
	settled map[string]map[uint64]bool
	failB   int
}

func newMemEscrow() *memEscrow {
	return &memEscrow{
		balance: map[uint64]int64{1: 100, 2: 50},
		escrow:  make(map[string]map[uint64]Assets),
		settled: make(map[string]map[uint64]bool),
	}
}

func (m *memEscrow) Lock(_ context.Context, pid uint64, id string, a Assets) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.escrow[id] == nil {
		m.escrow[id] = make(map[uint64]Assets)
	}
	if _, ok := m.escrow[id][pid]; ok {
		return nil
	}
	if m.balance[pid] < a.Gold {
		return fmt.Errorf("insufficient")
	}
	m.balance[pid] -= a.Gold
	m.escrow[id][pid] = a
	return nil
}

func (m *memEscrow) Refund(_ context.Context, pid uint64, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.escrow[id][pid]
	if !ok {
		return nil
	}
	m.balance[pid] += a.Gold
	delete(m.escrow[id], pid)
	return nil
}

func (m *memEscrow) Settle(_ context.Context, pid uint64, id string, credit Assets) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.settled[id] == nil {
		m.settled[id] = make(map[uint64]bool)
	}
	if m.settled[id][pid] {
		return nil
	}
	if _, ok := m.escrow[id][pid]; !ok {
		return errors.New("no escrow")
	}
	if pid == 2 && m.failB > 0 {
		m.failB--
		return errors.New("settle B failed")
	}
	delete(m.escrow[id], pid)
	m.balance[pid] += credit.Gold
	m.settled[id][pid] = true
	return nil
}

func TestServiceHappyPath(t *testing.T) {
	store, esc := newMemStore(), newMemEscrow()
	svc := NewService(store, esc)
	ctx := context.Background()

	tr, err := svc.Create(ctx, 1, 1, 1, 2, Assets{Gold: 10}, Assets{Gold: 5}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Accept(ctx, tr.ID, 2); err != nil {
		t.Fatal(err)
	}
	if err := svc.Settle(ctx, tr.ID); err != nil {
		t.Fatal(err)
	}
	if esc.balance[1] != 95 || esc.balance[2] != 55 {
		t.Fatalf("balances = %d,%d", esc.balance[1], esc.balance[2])
	}
	if err := svc.Settle(ctx, tr.ID); err != nil {
		t.Fatal("second settle should be no-op:", err)
	}
}

func TestCreateFailureRefundsA(t *testing.T) {
	store, esc := newMemStore(), newMemEscrow()
	store.failCreate = true
	svc := NewService(store, esc)
	if _, err := svc.Create(context.Background(), 1, 1, 1, 2, Assets{Gold: 10}, Assets{}, time.Minute); err == nil {
		t.Fatal("want create error")
	}
	if esc.balance[1] != 100 {
		t.Fatalf("A not refunded: %d", esc.balance[1])
	}
}

func TestAcceptFailureRefundsB(t *testing.T) {
	store, esc := newMemStore(), newMemEscrow()
	svc := NewService(store, esc)
	ctx := context.Background()
	tr, err := svc.Create(ctx, 1, 1, 1, 2, Assets{Gold: 10}, Assets{Gold: 5}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	store.failTransition = true
	if err := svc.Accept(ctx, tr.ID, 2); err == nil {
		t.Fatal("want accept error")
	}
	if esc.balance[2] != 50 {
		t.Fatalf("B not refunded: %d", esc.balance[2])
	}
	if got, _ := store.Get(ctx, tr.ID); got.State != ALocked {
		t.Fatalf("state = %s", got.State)
	}
}

func TestSettleResumesAfterPartialFailure(t *testing.T) {
	store, esc := newMemStore(), newMemEscrow()
	esc.failB = 1
	svc := NewService(store, esc)
	ctx := context.Background()
	tr, _ := svc.Create(ctx, 1, 1, 1, 2, Assets{Gold: 10}, Assets{Gold: 5}, time.Minute)
	if err := svc.Accept(ctx, tr.ID, 2); err != nil {
		t.Fatal(err)
	}
	if err := svc.Settle(ctx, tr.ID); err == nil {
		t.Fatal("want first settle error")
	}
	if got, _ := store.Get(ctx, tr.ID); got.State != Settling {
		t.Fatalf("state = %s, want settling", got.State)
	}
	if err := svc.Settle(ctx, tr.ID); err != nil {
		t.Fatal(err)
	}
	if esc.balance[1] != 95 || esc.balance[2] != 55 {
		t.Fatalf("balances = %d,%d", esc.balance[1], esc.balance[2])
	}
}

func TestCancelExpired(t *testing.T) {
	store, esc := newMemStore(), newMemEscrow()
	svc := NewService(store, esc)
	ctx := context.Background()
	tr, _ := svc.Create(ctx, 1, 1, 1, 2, Assets{Gold: 10}, Assets{Gold: 5}, -time.Second)
	if err := svc.Cancel(ctx, tr.ID); err != nil {
		t.Fatal(err)
	}
	if esc.balance[1] != 100 || esc.balance[2] != 50 {
		t.Fatalf("balances = %d,%d", esc.balance[1], esc.balance[2])
	}
	if got, _ := store.Get(ctx, tr.ID); got.State != Refunded {
		t.Fatalf("state = %s", got.State)
	}
}

func TestWrongParty(t *testing.T) {
	store, esc := newMemStore(), newMemEscrow()
	svc := NewService(store, esc)
	tr, _ := svc.Create(context.Background(), 1, 1, 1, 2, Assets{Gold: 1}, Assets{}, time.Minute)
	if err := svc.Accept(context.Background(), tr.ID, 3); !errors.Is(err, ErrWrongState) {
		t.Fatalf("want wrong state, got %v", err)
	}
}

func TestReconcileExpired(t *testing.T) {
	store, esc := newMemStore(), newMemEscrow()
	svc := NewService(store, esc)
	ctx := context.Background()
	tr, err := svc.Create(ctx, 1, 1, 1, 2, Assets{Gold: 10}, Assets{Gold: 5}, -time.Second)
	if err != nil {
		t.Fatal(err)
	}
	n, err := svc.Reconcile(ctx, 10)
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if got, _ := store.Get(ctx, tr.ID); got.State != Refunded {
		t.Fatalf("state = %s", got.State)
	}
	if esc.balance[1] != 100 {
		t.Fatalf("A balance = %d", esc.balance[1])
	}
}

func TestReconcileResumesSettling(t *testing.T) {
	store, esc := newMemStore(), newMemEscrow()
	esc.failB = 1
	svc := NewService(store, esc)
	ctx := context.Background()
	tr, _ := svc.Create(ctx, 1, 1, 1, 2, Assets{Gold: 10}, Assets{Gold: 5}, time.Minute)
	if err := svc.Accept(ctx, tr.ID, 2); err != nil {
		t.Fatal(err)
	}
	if err := svc.Settle(ctx, tr.ID); err == nil {
		t.Fatal("want first settle error")
	}
	n, err := svc.Reconcile(ctx, 10)
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if got, _ := store.Get(ctx, tr.ID); got.State != Settled {
		t.Fatalf("state = %s", got.State)
	}
	if esc.balance[1] != 95 || esc.balance[2] != 55 {
		t.Fatalf("balances = %d,%d", esc.balance[1], esc.balance[2])
	}
}
