package trade

import (
	"errors"
	"testing"
)

func TestHappyPath(t *testing.T) {
	tr := &Trade{ID: "t1", State: ALocked}
	if err := tr.Transition(BothLocked, 1); err != nil {
		t.Fatal(err)
	}
	if err := tr.Transition(Settling, 2); err != nil {
		t.Fatal(err)
	}
	if err := tr.Transition(Settled, 3); err != nil {
		t.Fatal(err)
	}
}

func TestIllegalAndStale(t *testing.T) {
	tr := &Trade{ID: "t1", State: ALocked}
	if err := tr.Transition(Settled, 1); !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("want illegal transition, got %v", err)
	}
	if err := tr.Transition(BothLocked, 2); err == nil {
		t.Fatal("want stale version error")
	}
}

func TestRefundPath(t *testing.T) {
	tr := &Trade{ID: "t1", State: BothLocked}
	if err := tr.Transition(Refunding, 1); err != nil {
		t.Fatal(err)
	}
	if err := tr.Transition(Refunded, 2); err != nil {
		t.Fatal(err)
	}
}
