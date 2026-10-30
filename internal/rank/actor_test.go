package rank

import (
	"encoding/json"
	"testing"

	"hapeserver/internal/actor"
)

func TestWorldRankUpdateAndStale(t *testing.T) {
	w := NewWorldRankActor(nil)
	if !w.Update(Update{PID: 1, Power: 100, Version: 1}) {
		t.Fatal("first update rejected")
	}
	if w.Update(Update{PID: 1, Power: 90, Version: 1}) {
		t.Fatal("stale update accepted")
	}
	if w.Update(Update{PID: 1, Power: 90, Version: 2}) == false {
		t.Fatal("newer update rejected")
	}
	if rank, ok := w.RankOf(1); !ok || rank != 1 {
		t.Fatalf("rank = %d,%v", rank, ok)
	}
	w.Update(Update{PID: 2, Power: 200, Version: 1})
	if rank, _ := w.RankOf(1); rank != 2 {
		t.Fatalf("rank after pid2 = %d, want 2", rank)
	}
	top := w.TopK(1)
	if len(top) != 1 || top[0].PID != 2 {
		t.Fatalf("top1 = %+v", top)
	}
}

func TestWorldRankReceive(t *testing.T) {
	w := NewWorldRankActor(nil)
	payload, _ := json.Marshal(Update{PID: 7, Power: 42, Version: 3})
	w.Receive(nil, &actor.Envelope{Kind: actor.KindRankUpdate, Payload: payload})
	if w.Len() != 1 {
		t.Fatalf("len = %d", w.Len())
	}
	w.Receive(nil, &actor.Envelope{Kind: actor.KindRankUpdate, Payload: []byte("{")})
	if w.Len() != 1 {
		t.Fatal("bad payload changed state")
	}
}

func TestWorldRankRebuild(t *testing.T) {
	w := NewWorldRankActor(nil)
	w.Update(Update{PID: 1, Power: 10, Version: 1})
	w.Rebuild([]Entry{{PID: 2, Power: 20}, {PID: 3, Power: 30}}, map[uint64]uint64{2: 5, 3: 5})
	if w.Len() != 2 {
		t.Fatalf("len = %d", w.Len())
	}
	if rank, _ := w.RankOf(3); rank != 1 {
		t.Fatalf("rank 3 = %d", rank)
	}
	if w.Update(Update{PID: 2, Power: 25, Version: 5}) {
		t.Fatal("stale after rebuild accepted")
	}
	if !w.Update(Update{PID: 2, Power: 25, Version: 6}) {
		t.Fatal("newer after rebuild rejected")
	}
}
