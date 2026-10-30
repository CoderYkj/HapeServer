package rank

import (
	"context"
	"testing"
)

type memProjection struct{ rows []PowerRow }

func (m memProjection) Load(context.Context, uint32, uint32) ([]PowerRow, error) {
	return m.rows, nil
}

func TestRebuildFromProjection(t *testing.T) {
	w := NewWorldRankActor(nil)
	w.Update(Update{PID: 9, Power: 1, Version: 1})

	n, err := w.RebuildFrom(context.Background(), memProjection{rows: []PowerRow{
		{PID: 1, Power: 100, Version: 7},
		{PID: 2, Power: 200, Version: 8},
	}}, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 || w.Len() != 2 {
		t.Fatalf("n=%d len=%d", n, w.Len())
	}
	if rank, _ := w.RankOf(2); rank != 1 {
		t.Fatalf("rank 2 = %d", rank)
	}
	if w.Update(Update{PID: 1, Power: 300, Version: 7}) {
		t.Fatal("stale update after rebuild accepted")
	}
	if !w.Update(Update{PID: 1, Power: 300, Version: 8}) {
		t.Fatal("newer update after rebuild rejected")
	}
}
