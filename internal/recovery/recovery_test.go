package recovery

import (
	"errors"
	"testing"

	"hapeserver/internal/snapshot"
	"hapeserver/internal/wal"
)

type recorder struct {
	snapshot []byte
	offsets  []uint64
	records  []string
}

func (r *recorder) ApplySnapshot(p []byte) error { r.snapshot = append([]byte(nil), p...); return nil }
func (r *recorder) ApplyRecord(off uint64, rec []byte) error {
	r.offsets = append(r.offsets, off)
	r.records = append(r.records, string(rec))
	return nil
}

func TestRecoverFromSnapshotAndTail(t *testing.T) {
	store, err := snapshot.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	log := wal.NewMemory()
	for _, s := range []string{"1", "2", "3", "4"} {
		if _, err := log.Append([]byte(s)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.Save(1, 1, 2, []byte("snap-2")); err != nil {
		t.Fatal(err)
	}

	var rec recorder
	last, err := Recover(store, log, 1, &rec)
	if err != nil {
		t.Fatal(err)
	}
	if string(rec.snapshot) != "snap-2" {
		t.Fatalf("snapshot = %q", rec.snapshot)
	}
	if len(rec.offsets) != 2 || rec.offsets[0] != 3 || rec.offsets[1] != 4 {
		t.Fatalf("offsets = %v", rec.offsets)
	}
	if last != 4 {
		t.Fatalf("last = %d, want 4", last)
	}
}

func TestRecoverWithoutSnapshot(t *testing.T) {
	store, _ := snapshot.NewStore(t.TempDir())
	log := wal.NewMemory()
	_, _ = log.Append([]byte("a"))
	_, _ = log.Append([]byte("b"))

	var rec recorder
	last, err := Recover(store, log, 1, &rec)
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.records) != 2 || rec.records[1] != "b" {
		t.Fatalf("records = %v", rec.records)
	}
	if last != 2 {
		t.Fatalf("last = %d", last)
	}
}

type failApply struct{}

func (failApply) ApplySnapshot([]byte) error       { return nil }
func (failApply) ApplyRecord(uint64, []byte) error { return errors.New("boom") }

func TestRecoverStopsOnApplyError(t *testing.T) {
	store, _ := snapshot.NewStore(t.TempDir())
	log := wal.NewMemory()
	_, _ = log.Append([]byte("a"))
	if _, err := Recover(store, log, 1, failApply{}); err == nil {
		t.Fatal("want error")
	}
}
