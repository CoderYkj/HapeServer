package wal

import "testing"

func TestAppendReplayTruncate(t *testing.T) {
	log := NewMemory()
	if _, err := log.Append([]byte("a")); err != nil {
		t.Fatal(err)
	}
	if _, err := log.Append([]byte("b")); err != nil {
		t.Fatal(err)
	}
	if err := log.Truncate(2); err != nil {
		t.Fatal(err)
	}
	if log.LastOffset() != 2 {
		t.Fatalf("last offset = %d, want 2", log.LastOffset())
	}
	off, err := log.Append([]byte("c"))
	if err != nil || off != 3 {
		t.Fatalf("append off=%d err=%v", off, err)
	}
	var offsets []uint64
	if err := log.Replay(func(off uint64, _ []byte) error {
		offsets = append(offsets, off)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(offsets) != 2 || offsets[0] != 2 || offsets[1] != 3 {
		t.Fatalf("offsets = %v", offsets)
	}
}
