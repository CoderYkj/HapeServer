// Package rank implements the P0 in-memory leaderboard. The skiplist is
// replaced with an ordered slice while task F02 is in progress; the public
// contract already matches the design document.
package rank

import (
	"bytes"
	"encoding/binary"
	"sort"
)

// Entry is one ranked player.
type Entry struct {
	PID   uint64
	Power int64
	Tie   int64
}

// Key is the 24-byte total order from the architecture document:
// [^power][tie][pid], big endian.
type Key [24]byte

// Order encodes an Entry into a Key. Implementations must be total orders.
type Order interface {
	Name() string
	Version() uint32
	Key(Entry) Key
}

// DefaultOrder is power desc, tie asc (always 0), pid asc.
type DefaultOrder struct{}

func (DefaultOrder) Name() string    { return "default" }
func (DefaultOrder) Version() uint32 { return 1 }

func (DefaultOrder) Key(e Entry) Key {
	var k Key
	binary.BigEndian.PutUint64(k[0:8], ^uint64(e.Power))
	binary.BigEndian.PutUint64(k[8:16], uint64(e.Tie)^(1<<63))
	binary.BigEndian.PutUint64(k[16:24], e.PID)
	return k
}

// List is an ordered leaderboard.
type List struct {
	order Order
	items []Entry
}

// NewList builds a list for the given order.
func NewList(order Order) *List {
	if order == nil {
		order = DefaultOrder{}
	}
	return &List{order: order}
}

// Add inserts or replaces e. O(n) until F02 lands.
func (l *List) Add(e Entry) {
	l.Remove(e.PID)
	l.items = append(l.items, e)
	sort.Slice(l.items, func(i, j int) bool {
		ki := l.order.Key(l.items[i])
		kj := l.order.Key(l.items[j])
		return bytes.Compare(ki[:], kj[:]) < 0
	})
}

// Remove deletes pid if present.
func (l *List) Remove(pid uint64) {
	for i, e := range l.items {
		if e.PID == pid {
			l.items = append(l.items[:i], l.items[i+1:]...)
			return
		}
	}
}

// RankOf returns the 1-based unique rank.
func (l *List) RankOf(pid uint64) (int, bool) {
	for i, e := range l.items {
		if e.PID == pid {
			return i + 1, true
		}
	}
	return 0, false
}

// TopK returns at most k leading entries.
func (l *List) TopK(k int) []Entry {
	if k < 0 {
		k = 0
	}
	if k > len(l.items) {
		k = len(l.items)
	}
	out := make([]Entry, k)
	copy(out, l.items[:k])
	return out
}

// Len reports entries.
func (l *List) Len() int { return len(l.items) }

// KeyOf exposes the encoded key for a player.
func (l *List) KeyOf(e Entry) Key { return l.order.Key(e) }
