package snapshot

import (
	"errors"
	"os"
	"testing"
)

func TestSaveLoadAndRetention(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		payload := []byte{byte('a' + i)}
		if _, err := s.Save(7, 1, uint64(i+1), payload); err != nil {
			t.Fatal(err)
		}
	}
	files, _ := os.ReadDir(dir)
	if len(files) != 3 {
		t.Fatalf("files = %d, want 3", len(files))
	}
	h, payload, _, err := s.LoadLatest(7)
	if err != nil {
		t.Fatal(err)
	}
	if h.WALOffset != 5 || string(payload) != "e" {
		t.Fatalf("loaded offset=%d payload=%q", h.WALOffset, payload)
	}
}

func TestCorruptLatestFallsBack(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStore(dir)
	if _, err := s.Save(1, 1, 1, []byte("first")); err != nil {
		t.Fatal(err)
	}
	path, err := s.Save(1, 1, 2, []byte("second"))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	b[len(b)-1] ^= 0xff
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	h, payload, _, err := s.LoadLatest(1)
	if err != nil {
		t.Fatal(err)
	}
	if h.WALOffset != 1 || string(payload) != "first" {
		t.Fatalf("fallback offset=%d payload=%q", h.WALOffset, payload)
	}
}

func TestMissing(t *testing.T) {
	s, _ := NewStore(t.TempDir())
	if _, _, _, err := s.LoadLatest(9); !errors.Is(err, ErrNoSnapshot) {
		t.Fatalf("want ErrNoSnapshot, got %v", err)
	}
}
