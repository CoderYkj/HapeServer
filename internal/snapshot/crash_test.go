package snapshot

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestTruncatedNewestFallsBack(t *testing.T) {
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
	if err := os.WriteFile(path, b[:10], 0o644); err != nil {
		t.Fatal(err)
	}
	h, payload, _, err := s.LoadLatest(1)
	if err != nil {
		t.Fatal(err)
	}
	if h.WALOffset != 1 || string(payload) != "first" {
		t.Fatalf("offset=%d payload=%q", h.WALOffset, payload)
	}
}

func TestTmpFileIgnored(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStore(dir)
	tmp := filepath.Join(dir, "snap-1-1-1-00000000000000000001.bin.tmp")
	if err := os.WriteFile(tmp, []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.LoadLatest(1); !errors.Is(err, ErrNoSnapshot) {
		t.Fatalf("want ErrNoSnapshot, got %v", err)
	}
}

func TestAllGenerationsCorrupt(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStore(dir)
	path, _ := s.Save(1, 1, 1, []byte("x"))
	b, _ := os.ReadFile(path)
	b[len(b)-1] ^= 0xff
	_ = os.WriteFile(path, b, 0o644)
	if _, _, _, err := s.LoadLatest(1); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("want ErrCorrupt, got %v", err)
	}
}
