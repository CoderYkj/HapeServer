package timer

import (
	"testing"
	"time"
)

func TestAdvanceFiresInOrder(t *testing.T) {
	w := New(time.Unix(0, 0))
	var got []string
	w.After(2*time.Second, func() { got = append(got, "b") })
	w.After(1*time.Second, func() { got = append(got, "a") })

	w.Advance(time.Second)
	if len(got) != 1 || got[0] != "a" {
		t.Fatalf("after 1s got %v", got)
	}
	w.Advance(time.Second)
	if len(got) != 2 || got[1] != "b" {
		t.Fatalf("after 2s got %v", got)
	}
}

func TestCancel(t *testing.T) {
	w := New(time.Unix(0, 0))
	id := w.After(time.Second, func() { t.Fatal("should not fire") })
	w.Cancel(id)
	w.Advance(2 * time.Second)
	if w.Len() != 0 {
		t.Fatalf("pending = %d", w.Len())
	}
}
