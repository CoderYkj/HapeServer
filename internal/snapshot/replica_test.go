package snapshot

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReplicaSetSaveAndLoad(t *testing.T) {
	dirs := []string{t.TempDir(), t.TempDir(), t.TempDir()}
	rs, err := NewReplicaSet(dirs, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rs.Save(1, 1, 10, []byte("v1")); err != nil {
		t.Fatal(err)
	}
	for _, d := range dirs {
		files, _ := filepath.Glob(filepath.Join(d, "snap-*.bin"))
		if len(files) != 1 {
			t.Fatalf("dir %s has %d files", d, len(files))
		}
	}

	// One replica can be ahead; LoadLatest picks the highest valid offset.
	if _, err := rs.stores[0].Save(1, 1, 11, []byte("v2")); err != nil {
		t.Fatal(err)
	}
	h, payload, _, err := rs.LoadLatest(1)
	if err != nil || h.WALOffset != 11 || string(payload) != "v2" {
		t.Fatalf("h=%+v payload=%q err=%v", h, payload, err)
	}

	// Corrupt the best copy: fall back to the other replica.
	files, _ := filepath.Glob(filepath.Join(dirs[0], "snap-*-11-*.bin"))
	if len(files) != 1 {
		t.Fatalf("files = %v", files)
	}
	if err := os.WriteFile(files[0], []byte("broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	h, payload, _, err = rs.LoadLatest(1)
	if err != nil || h.WALOffset != 10 || string(payload) != "v1" {
		t.Fatalf("fallback h=%+v payload=%q err=%v", h, payload, err)
	}
}

func TestReplicaSetQuorum(t *testing.T) {
	dirs := []string{t.TempDir(), t.TempDir(), t.TempDir()}
	rs, err := NewReplicaSet(dirs, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dirs[1]); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dirs[2]); err != nil {
		t.Fatal(err)
	}
	if _, err := rs.Save(1, 1, 1, []byte("x")); err == nil {
		t.Fatal("want quorum error")
	}
}
