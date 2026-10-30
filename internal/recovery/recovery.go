// Package recovery rebuilds a shard from its latest snapshot and WAL tail.
package recovery

import (
	"errors"
	"fmt"

	"hapeserver/internal/snapshot"
	"hapeserver/internal/wal"
)

// Applier restores state from a snapshot payload and then from WAL records.
type Applier interface {
	ApplySnapshot(payload []byte) error
	ApplyRecord(offset uint64, record []byte) error
}

// Loader loads the newest valid snapshot for a shard.
type Loader interface {
	LoadLatest(shard uint32) (snapshot.Header, []byte, string, error)
}

// Recover loads the newest valid snapshot for shard (if any), applies it,
// then replays WAL records after the snapshot's WAL offset. It returns the
// last applied WAL offset.
func Recover(store Loader, log wal.Log, shard uint32, applier Applier) (uint64, error) {
	start := uint64(0)
	h, payload, _, err := store.LoadLatest(shard)
	switch {
	case err == nil:
		if err := applier.ApplySnapshot(payload); err != nil {
			return 0, fmt.Errorf("apply snapshot: %w", err)
		}
		start = h.WALOffset
	case errors.Is(err, snapshot.ErrNoSnapshot):
		// Fresh shard: replay the whole retained WAL.
	default:
		return 0, fmt.Errorf("load snapshot: %w", err)
	}

	last := start
	err = log.Replay(func(offset uint64, record []byte) error {
		if offset <= start {
			return nil
		}
		if err := applier.ApplyRecord(offset, record); err != nil {
			return err
		}
		last = offset
		return nil
	})
	if err != nil {
		return last, fmt.Errorf("replay wal: %w", err)
	}
	return last, nil
}
