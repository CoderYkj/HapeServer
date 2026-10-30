package recovery

import (
	"encoding/json"
	"errors"
	"testing"

	"hapeserver/internal/snapshot"
	"hapeserver/internal/wal"
)

type jsonApplier struct {
	offsets []uint64
}

func (j *jsonApplier) ApplySnapshot(p []byte) error {
	if !json.Valid(p) {
		return errors.New("bad snapshot")
	}
	return nil
}

func (j *jsonApplier) ApplyRecord(off uint64, rec []byte) error {
	if !json.Valid(rec) {
		return errors.New("corrupt record")
	}
	j.offsets = append(j.offsets, off)
	return nil
}

func TestBoundarySkipsSnapshotOffset(t *testing.T) {
	store, _ := snapshot.NewStore(t.TempDir())
	log := wal.NewMemory()
	for _, s := range []string{"1", "2", "3"} {
		_, _ = log.Append([]byte(s))
	}
	if _, err := store.Save(1, 1, 2, []byte("[]")); err != nil {
		t.Fatal(err)
	}
	var applier jsonApplier
	last, err := Recover(store, log, 1, &applier)
	if err != nil {
		t.Fatal(err)
	}
	if len(applier.offsets) != 1 || applier.offsets[0] != 3 {
		t.Fatalf("offsets = %v", applier.offsets)
	}
	if last != 3 {
		t.Fatalf("last = %d", last)
	}
}

func TestCorruptRecordStops(t *testing.T) {
	store, _ := snapshot.NewStore(t.TempDir())
	log := wal.NewMemory()
	_, _ = log.Append([]byte("1"))
	_, _ = log.Append([]byte("{"))
	_, _ = log.Append([]byte("3"))
	var applier jsonApplier
	last, err := Recover(store, log, 1, &applier)
	if err == nil {
		t.Fatal("want error")
	}
	if len(applier.offsets) != 1 || applier.offsets[0] != 1 {
		t.Fatalf("offsets = %v", applier.offsets)
	}
	if last != 1 {
		t.Fatalf("last = %d, want 1", last)
	}
}
