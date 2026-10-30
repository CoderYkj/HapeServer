package battle

import (
	"context"
	"testing"
)

func TestResolve(t *testing.T) {
	if got := Resolve(1, 2, 100, 90); got != 1 {
		t.Fatalf("attacker win: %d", got)
	}
	if got := Resolve(1, 2, 90, 100); got != 2 {
		t.Fatalf("defender win: %d", got)
	}
	if got := Resolve(1, 2, 100, 100); got != 2 {
		t.Fatalf("tie should favor defender: %d", got)
	}
}

func TestSubmitAndGet(t *testing.T) {
	svc := NewService(nil)
	b, err := svc.Submit(context.Background(), 1, 1, 10, 20, 500, 400)
	if err != nil {
		t.Fatal(err)
	}
	if b.Winner != 10 || b.State != Settled {
		t.Fatalf("battle = %+v", b)
	}
	got, err := svc.Get(context.Background(), b.ID)
	if err != nil || got.Winner != 10 {
		t.Fatalf("get: %+v err=%v", got, err)
	}
}