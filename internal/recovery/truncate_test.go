package recovery

import (
	"testing"

	"hapeserver/internal/snapshot"
	"hapeserver/internal/wal"
)

func TestRecoverAfterTruncate(t *testing.T) {
	store, _ := snapshot.NewStore(t.TempDir())
	log := wal.NewMemory()
	for _, s := range []string{"1", "2", "3", "4"} {
		_, _ = log.Append([]byte(s))
	}
	if _, err := store.Save(1, 1, 2, []byte("[]")); err != nil {
		t.Fatal(err)
	}
	if err := log.Truncate(3); err != nil {
		t.Fatal(err)
	}
	var applier jsonApplier
	last, err := Recover(store, log, 1, &applier)
	if err != nil {
		t.Fatal(err)
	}
	if len(applier.offsets) != 2 || applier.offsets[0] != 3 || applier.offsets[1] != 4 {
		t.Fatalf("offsets = %v", applier.offsets)
	}
	if last != 4 {
		t.Fatalf("last = %d", last)
	}
}
