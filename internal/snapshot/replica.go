package snapshot

import (
	"errors"
	"fmt"
	"strings"
)

// Set is a snapshot destination: one local store or a replicated set.
type Set interface {
	Save(shard, epoch uint32, walOffset uint64, payload []byte) (string, error)
	LoadLatest(shard uint32) (Header, []byte, string, error)
}

// ReplicaSet writes every snapshot to local stores and peer worlds, and
// requires quorum success.
type ReplicaSet struct {
	stores []*Store
	peers  []*peerReplica
	quorum int
}

// NewReplicaSet opens local directories with no peers.
func NewReplicaSet(dirs []string, quorum int) (*ReplicaSet, error) {
	return NewReplicaSetWithPeers(dirs, quorum, nil, "")
}

// NewReplicaSetWithPeers opens local directories and peer snapshot endpoints.
func NewReplicaSetWithPeers(dirs []string, quorum int, peers []string, token string) (*ReplicaSet, error) {
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
	rs := &ReplicaSet{stores: stores}
	for _, base := range peers {
		base = strings.TrimRight(strings.TrimSpace(base), "/")
		if base == "" {
			continue
		}
		rs.peers = append(rs.peers, newPeerReplica(base, token))
	}
	total := len(stores) + len(rs.peers)
	if quorum <= 0 {
		quorum = total/2 + 1
	}
	if quorum > total {
		return nil, fmt.Errorf("snapshot: quorum %d > replicas %d", quorum, total)
	}
	rs.quorum = quorum
	return rs, nil
}

// Replicas reports the total number of replicas.
func (r *ReplicaSet) Replicas() int { return len(r.stores) + len(r.peers) }

// Quorum reports the write quorum.
func (r *ReplicaSet) Quorum() int { return r.quorum }

// Primary returns the first local store, used by the peer HTTP endpoint.
func (r *ReplicaSet) Primary() *Store { return r.stores[0] }

// Save writes to every replica and succeeds when quorum acked.
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
	h := Header{Version: 1, Shard: shard, Epoch: epoch, WALOffset: walOffset, Count: uint32(len(payload))}
	body := Encode(h, payload)
	for _, p := range r.peers {
		if err := p.save(body); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		ok++
		if firstPath == "" {
			firstPath = p.base + " (remote)"
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

	consider := func(h Header, payload []byte, path string, err error) {
		if err != nil {
			if !errors.Is(err, ErrNoSnapshot) {
				sawCorrupt = true
				lastErr = err
			}
			return
		}
		if !found || h.WALOffset > best.WALOffset || (h.WALOffset == best.WALOffset && h.Epoch > best.Epoch) {
			best, bestPayload, bestPath, found = h, payload, path, true
		}
	}

	for _, s := range r.stores {
		h, payload, path, err := s.LoadLatest(shard)
		consider(h, payload, path, err)
	}
	for _, p := range r.peers {
		h, payload, err := p.load(shard)
		if err != nil {
			// An unreachable peer is "unavailable", not "corrupt": the world
			// must still start before its peers do.
			lastErr = err
			continue
		}
		consider(h, payload, p.base+" (remote)", nil)
	}
	if found {
		return best, bestPayload, bestPath, nil
	}
	if sawCorrupt {
		return Header{}, nil, "", fmt.Errorf("no valid snapshot replica: %w", lastErr)
	}
	return Header{}, nil, "", ErrNoSnapshot
}
