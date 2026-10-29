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

	var got []string
	if err := log.Replay(func(_ uint64, r []byte) error {
		got = append(got, string(r))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("replay = %v", got)
	}

	if err := log.Truncate(2); err != nil {
		t.Fatal(err)
	}
	if log.LastOffset() != 1 {
		t.Fatalf("last offset = %d, want 1", log.LastOffset())
	}
}
