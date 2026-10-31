package snapshot

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ErrNoSnapshot means the shard has no snapshot yet.
var ErrNoSnapshot = errors.New("no snapshot")

// Store persists one shard's snapshots and keeps the newest generations.
type Store struct {
	dir  string
	keep int
	mu   sync.Mutex
}

// NewStore creates dir if needed. keep defaults to 3.
func NewStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Store{dir: dir, keep: 3}, nil
}

// Dir returns the snapshot directory.
func (s *Store) Dir() string { return s.dir }

// Save writes one snapshot atomically and prunes old generations.
func (s *Store) Save(shard, epoch uint32, walOffset uint64, payload []byte) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	h := Header{
		Version:   1,
		Shard:     shard,
		Epoch:     epoch,
		WALOffset: walOffset,
		Count:     uint32(len(payload)),
	}
	name := fmt.Sprintf("snap-%d-%d-%d-%020d.bin", shard, epoch, walOffset, time.Now().UnixNano())
	path := filepath.Join(s.dir, name)
	tmp := path + ".tmp"

	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return "", err
	}
	if err := EncodeTo(f, h, payload); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return "", err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	s.pruneLocked(shard)
	return path, nil
}

// LoadLatest returns the newest valid snapshot for shard. Corrupt
// generations are skipped so a torn write cannot take the shard down.
func (s *Store) LoadLatest(shard uint32) (Header, []byte, string, error) {
	files, err := s.listLocked(shard)
	if err != nil {
		return Header{}, nil, "", err
	}
	if len(files) == 0 {
		return Header{}, nil, "", ErrNoSnapshot
	}
	var lastErr error = ErrNoSnapshot
	var best Header
	var bestPayload []byte
	var bestPath string
	found := false
	for i := range files {
		b, err := os.ReadFile(files[i].path)
		if err != nil {
			lastErr = err
			continue
		}
		h, payload, err := Decode(b)
		if err != nil {
			lastErr = err
			continue
		}
		if !found || h.WALOffset > best.WALOffset || (h.WALOffset == best.WALOffset && h.Epoch > best.Epoch) {
			best, bestPayload, bestPath, found = h, payload, files[i].path, true
		}
	}
	if found {
		return best, bestPayload, bestPath, nil
	}
	return Header{}, nil, "", fmt.Errorf("no valid snapshot: %w", lastErr)
}

type fileInfo struct {
	path string
	ts   int64
}

func (s *Store) listLocked(shard uint32) ([]fileInfo, error) {
	pattern := filepath.Join(s.dir, fmt.Sprintf("snap-%d-*.bin", shard))
	paths, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}
	out := make([]fileInfo, 0, len(paths))
	for _, p := range paths {
		ts, err := snapshotTimestamp(filepath.Base(p))
		if err != nil {
			continue
		}
		out = append(out, fileInfo{path: p, ts: ts})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ts < out[j].ts })
	return out, nil
}

func (s *Store) pruneLocked(shard uint32) {
	files, err := s.listLocked(shard)
	if err != nil || len(files) <= s.keep {
		return
	}
	for _, f := range files[:len(files)-s.keep] {
		_ = os.Remove(f.path)
	}
}

func snapshotTimestamp(name string) (int64, error) {
	base := strings.TrimSuffix(name, ".bin")
	parts := strings.Split(base, "-")
	if len(parts) < 5 {
		return 0, fmt.Errorf("bad snapshot name %q", name)
	}
	return strconv.ParseInt(parts[len(parts)-1], 10, 64)
}
