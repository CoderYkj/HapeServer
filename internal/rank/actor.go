package rank

import (
	"encoding/json"
	"sync"

	"hapeserver/internal/actor"
)

// Update is one power change emitted by PlayerActor.
type Update struct {
	PID     uint64 `json:"pid"`
	Power   int64  `json:"power"`
	Tie     int64  `json:"tie"`
	Version uint64 `json:"version"`
}

// WorldRankActor owns one world's leaderboard. Methods are safe for tests and
// for a single actor goroutine; the mutex is not on the hot path of a real
// deployment because one actor serializes calls.
type WorldRankActor struct {
	mu       sync.Mutex
	list     *List
	versions map[uint64]uint64
}

// NewWorldRankActor builds an empty leaderboard.
func NewWorldRankActor(order Order) *WorldRankActor {
	return &WorldRankActor{
		list:     NewList(order),
		versions: make(map[uint64]uint64),
	}
}

// Update applies u and reports whether it was accepted (false = stale).
func (w *WorldRankActor) Update(u Update) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if v, ok := w.versions[u.PID]; ok && u.Version <= v {
		return false
	}
	w.versions[u.PID] = u.Version
	w.list.Add(Entry{PID: u.PID, Power: u.Power, Tie: u.Tie})
	return true
}

// TopK returns the leading entries.
func (w *WorldRankActor) TopK(k int) []Entry {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.list.TopK(k)
}

// RankOf returns the unique 1-based rank.
func (w *WorldRankActor) RankOf(pid uint64) (int, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.list.RankOf(pid)
}

// Len reports the number of ranked players.
func (w *WorldRankActor) Len() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.list.Len()
}

// Rebuild replaces the board from a projection. entries must carry the
// player's current version in versions (missing = 0).
func (w *WorldRankActor) Rebuild(entries []Entry, versions map[uint64]uint64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.list = NewList(w.list.order)
	w.versions = make(map[uint64]uint64, len(versions))
	for pid, v := range versions {
		w.versions[pid] = v
	}
	for _, e := range entries {
		w.list.Add(e)
	}
}

// Receive handles rank updates delivered as envelopes.
func (w *WorldRankActor) Receive(_ actor.Context, env *actor.Envelope) {
	if env.Kind != actor.KindRankUpdate {
		return
	}
	var u Update
	if err := json.Unmarshal(env.Payload, &u); err != nil {
		return
	}
	w.Update(u)
}
