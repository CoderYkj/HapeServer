package world

import (
	"errors"
	"testing"
)

func richPlayer() *PlayerActor {
	p := NewPlayer(1, 1)
	p.State.Resources = Assets{Gold: 100, Wood: 50, Food: 20}
	return p
}

func TestLockAndRefund(t *testing.T) {
	p := richPlayer()
	if err := p.LockEscrow("t1", Assets{Gold: 30, Wood: 10}); err != nil {
		t.Fatal(err)
	}
	if p.State.Resources.Gold != 70 || p.State.Resources.Wood != 40 {
		t.Fatalf("resources = %+v", p.State.Resources)
	}
	if err := p.LockEscrow("t1", Assets{Gold: 30, Wood: 10}); err != nil {
		t.Fatal(err)
	}
	if err := p.RefundEscrow("t1"); err != nil {
		t.Fatal(err)
	}
	if p.State.Resources.Gold != 100 || p.State.Resources.Wood != 50 {
		t.Fatalf("after refund = %+v", p.State.Resources)
	}
	if err := p.RefundEscrow("t1"); err != nil {
		t.Fatal(err)
	}
}

func TestInsufficient(t *testing.T) {
	p := richPlayer()
	if err := p.LockEscrow("t1", Assets{Gold: 101}); !errors.Is(err, ErrInsufficient) {
		t.Fatalf("want insufficient, got %v", err)
	}
	if len(p.State.Escrow) != 0 {
		t.Fatal("escrow changed on failure")
	}
}

func TestSettle(t *testing.T) {
	p := richPlayer()
	if err := p.LockEscrow("t1", Assets{Gold: 20}); err != nil {
		t.Fatal(err)
	}
	if err := p.SettleEscrow("t1", Assets{Wood: 5}); err != nil {
		t.Fatal(err)
	}
	if p.State.Resources.Gold != 80 || p.State.Resources.Wood != 55 {
		t.Fatalf("resources = %+v", p.State.Resources)
	}
	if err := p.SettleEscrow("t1", Assets{Wood: 5}); err != nil {
		t.Fatalf("repeat settle should be no-op: %v", err)
	}
	if p.State.Resources.Wood != 55 {
		t.Fatalf("repeat settle credited twice: %+v", p.State.Resources)
	}
	if err := p.RefundEscrow("t1"); !errors.Is(err, ErrTradeClosed) {
		t.Fatalf("want closed, got %v", err)
	}
}

func TestSettleWithoutEscrow(t *testing.T) {
	p := richPlayer()
	if err := p.SettleEscrow("missing", Assets{Gold: 1}); !errors.Is(err, ErrNoEscrow) {
		t.Fatalf("want no escrow, got %v", err)
	}
}
