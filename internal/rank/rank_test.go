package rank

import (
	"bytes"
	"math/rand"
	"sort"
	"testing"
)

func TestDefaultOrderAndRank(t *testing.T) {
	l := NewList(DefaultOrder{})
	l.Add(Entry{PID: 3, Power: 90})
	l.Add(Entry{PID: 2, Power: 100})
	l.Add(Entry{PID: 1, Power: 100})

	if got, _ := l.RankOf(1); got != 1 {
		t.Fatalf("pid 1 rank = %d, want 1", got)
	}
	if got, _ := l.RankOf(2); got != 2 {
		t.Fatalf("pid 2 rank = %d, want 2", got)
	}
	if got, _ := l.RankOf(3); got != 3 {
		t.Fatalf("pid 3 rank = %d, want 3", got)
	}

	top := l.TopK(2)
	if len(top) != 2 || top[0].PID != 1 || top[1].PID != 2 {
		t.Fatalf("top2 = %+v", top)
	}
}

func TestAddReplacesAndRemove(t *testing.T) {
	l := NewList(nil)
	l.Add(Entry{PID: 1, Power: 10})
	l.Add(Entry{PID: 1, Power: 20})
	if l.Len() != 1 {
		t.Fatalf("len = %d, want 1", l.Len())
	}
	l.Remove(1)
	if l.Len() != 0 {
		t.Fatalf("len after remove = %d", l.Len())
	}
}

// TestAgainstSortedModel is PT-01: random add/remove/rank/top-k against a
// sorted-slice model.
func TestAgainstSortedModel(t *testing.T) {
	l := NewList(nil)
	model := make(map[uint64]Entry)
	rnd := rand.New(rand.NewSource(42))
	order := DefaultOrder{}

	for i := 0; i < 20000; i++ {
		pid := uint64(rnd.Intn(500) + 1)
		switch rnd.Intn(4) {
		case 0, 1, 2:
			e := Entry{PID: pid, Power: int64(rnd.Intn(1000)), Tie: int64(rnd.Intn(3))}
			l.Add(e)
			model[pid] = e
		default:
			l.Remove(pid)
			delete(model, pid)
		}
		if i%97 == 0 {
			checkModel(t, l, model, order)
		}
	}
	checkModel(t, l, model, order)
}

func checkModel(t *testing.T, l *List, model map[uint64]Entry, order Order) {
	t.Helper()
	want := make([]Entry, 0, len(model))
	for _, e := range model {
		want = append(want, e)
	}
	sort.Slice(want, func(i, j int) bool {
		a, b := order.Key(want[i]), order.Key(want[j])
		return bytes.Compare(a[:], b[:]) < 0
	})

	if l.Len() != len(want) {
		t.Fatalf("len = %d, want %d", l.Len(), len(want))
	}
	got := l.TopK(len(want) + 8)
	if len(got) != len(want) {
		t.Fatalf("topk len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].PID != want[i].PID || got[i].Power != want[i].Power || got[i].Tie != want[i].Tie {
			t.Fatalf("pos %d = %+v, want %+v", i, got[i], want[i])
		}
		rank, ok := l.RankOf(want[i].PID)
		if !ok || rank != i+1 {
			t.Fatalf("rank(%d) = %d,%v want %d", want[i].PID, rank, ok, i+1)
		}
	}
}

func TestRemoveAbsent(t *testing.T) {
	l := NewList(nil)
	l.Add(Entry{PID: 1, Power: 1})
	l.Remove(999)
	if l.Len() != 1 {
		t.Fatalf("len = %d", l.Len())
	}
}

func BenchmarkAdd100k(b *testing.B) {
	l := NewList(nil)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l.Add(Entry{PID: uint64(i + 1), Power: int64(i % 10000)})
	}
}

func BenchmarkRankOf(b *testing.B) {
	l := NewList(nil)
	for i := 0; i < 100000; i++ {
		l.Add(Entry{PID: uint64(i + 1), Power: int64(i % 10000)})
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = l.RankOf(uint64(i%100000 + 1))
	}
}
