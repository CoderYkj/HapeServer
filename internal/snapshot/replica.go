package snapshot

import (
	"errors"
	"fmt"
)

// Set is a snapshot destination: one local store or a replicated set.
type Set interface {
	Save(shard, epoch uint32, walOffset uint64, payload []byte) (string, error)
	LoadLatest(shard uint32) (Header, []byte, string, error)
}

// ReplicaSet writes every snapshot to N stores and requires quorum success.
type ReplicaSet struct {
	stores []*Store
	quorum int
}

// NewReplicaSet opens each directory. quorum <= 0 defaults to len/2+1.
func NewReplicaSet(dirs []string, quorum int) (*ReplicaSet, error) {
	if len(dirs) == 0 {
		return nil, errors.New("snapshot: no replica directories")
	}
	seen := make(map[string]bool, len(dirs))
	stores := make([]*Store, 0, len(dirs))
	for _, dir := range dirs {
		if seen[dir] {
			return nil, fmt.Errorf("snapshot: duplicate replica dir %q", dir)
		}
		seen[dir] = true
		s, err := NewStore(dir)
		if err != nil {
			return nil, err
		}
		stores = append(stores, s)
	}
	if quorum <= 0 {
		quorum = len(stores)/2 + 1
	}
	if quorum > len(stores) {
		return nil, fmt.Errorf("snapshot: quorum %d > replicas %d", quorum, len(stores))
	}
	return &ReplicaSet{stores: stores, quorum: quorum}, nil
}

// Replicas reports the number of stores.
func (r *ReplicaSet) Replicas() int { return len(r.stores) }

// Quorum reports the write quorum.
func (r *ReplicaSet) Quorum() int { return r.quorum }

// Save writes to all replicas and succeeds when quorum acked.
func (r *ReplicaSet) Save(shard, epoch uint32, walOffset uint64, payload []byte) (string, error) {
	var firstPath string
	var firstErr error
	ok := 0
	for _, s := range r.stores {
		path, err := s.Save(shard, epoch, walOffset, payload)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		ok++
		if firstPath == "" {
			firstPath = path
		}
	}
	if ok < r.quorum {
		return "", fmt.Errorf("snapshot quorum %d/%d: %w", ok, r.quorum, firstErr)
	}
	return firstPath, nil
}

// LoadLatest picks the valid replica with the highest WAL offset.
func (r *ReplicaSet) LoadLatest(shard uint32) (Header, []byte, string, error) {
	var best Header
	var bestPayload []byte
	var bestPath string
	found := false
	sawCorrupt := false
	var lastErr error
	for _, s := range r.stores {
		h, payload, path, err := s.LoadLatest(shard)
		if err != nil {
			if !errors.Is(err, ErrNoSnapshot) {
				sawCorrupt = true
				lastErr = err
			}
			continue
		}
		if !found || h.WALOffset > best.WALOffset || (h.WALOffset == best.WALOffset && h.Epoch > best.Epoch) {
			best, bestPayload, bestPath, found = h, payload, path, true
		}
	}
	if found {
		return best, bestPayload, bestPath, nil
	}
	if sawCorrupt {
		return Header{}, nil, "", fmt.Errorf("no valid snapshot replica: %w", lastErr)
	}
	return Header{}, nil, "", ErrNoSnapshot
}
