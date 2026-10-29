package rank

import "testing"

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
