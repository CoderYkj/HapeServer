// Package rank implements the leaderboard used by WorldRankActor.
//
// List is an order-statistic skiplist keyed by the 24-byte RankKey from the
// architecture document. It supports O(log n) insert, delete, rank and
// top-k. A List is owned by one actor goroutine and is not safe for
// concurrent use.
package rank

import (
	"bytes"
	"encoding/binary"
	"math/rand"
)

const (
	maxLevel = 32
	levelP   = 0.25
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

type node struct {
	key  Key
	next []*node
	span []int
}

// List is an order-statistic skiplist.
type List struct {
	order  Order
	head   *node
	level  int
	length int
	rnd    *rand.Rand
	keys   map[uint64]Key
}

// NewList builds a list for the given order.
func NewList(order Order) *List {
	if order == nil {
		order = DefaultOrder{}
	}
	return &List{
		order: order,
		head:  &node{next: make([]*node, maxLevel), span: make([]int, maxLevel)},
		level: 1,
		rnd:   rand.New(rand.NewSource(1)),
		keys:  make(map[uint64]Key),
	}
}

// Add inserts e, replacing any previous entry with the same PID.
func (l *List) Add(e Entry) {
	k := l.order.Key(e)
	if old, ok := l.keys[e.PID]; ok {
		if old == k {
			return
		}
		l.removeKey(old)
	}

	update := make([]*node, maxLevel)
	rank := make([]int, maxLevel)
	x := l.head
	for i := l.level - 1; i >= 0; i-- {
		if i == l.level-1 {
			rank[i] = 0
		} else {
			rank[i] = rank[i+1]
		}
		for x.next[i] != nil && bytes.Compare(x.next[i].key[:], k[:]) < 0 {
			rank[i] += x.span[i]
			x = x.next[i]
		}
		update[i] = x
	}

	newLevel := l.randomLevel()
	if newLevel > l.level {
		for i := l.level; i < newLevel; i++ {
			update[i] = l.head
			rank[i] = 0
			l.head.span[i] = l.length
		}
		l.level = newLevel
	}

	n := &node{
		key:  k,
		next: make([]*node, newLevel),
		span: make([]int, newLevel),
	}
	for i := 0; i < newLevel; i++ {
		n.next[i] = update[i].next[i]
		update[i].next[i] = n
		n.span[i] = update[i].span[i] - (rank[0] - rank[i])
		update[i].span[i] = (rank[0] - rank[i]) + 1
	}
	for i := newLevel; i < l.level; i++ {
		update[i].span[i]++
	}
	l.length++
	l.keys[e.PID] = k
}

// Remove deletes pid if present.
func (l *List) Remove(pid uint64) {
	if k, ok := l.keys[pid]; ok {
		l.removeKey(k)
	}
}

func (l *List) removeKey(k Key) {
	update := make([]*node, maxLevel)
	x := l.head
	for i := l.level - 1; i >= 0; i-- {
		for x.next[i] != nil && bytes.Compare(x.next[i].key[:], k[:]) < 0 {
			x = x.next[i]
		}
		update[i] = x
	}

	target := update[0].next[0]
	if target == nil || target.key != k {
		return
	}
	for i := 0; i < l.level; i++ {
		if update[i].next[i] == target {
			update[i].next[i] = target.next[i]
			update[i].span[i] += target.span[i] - 1
		} else {
			update[i].span[i]--
		}
	}
	l.length--
	delete(l.keys, targetKeyPID(target.key))
	for l.level > 1 && l.head.next[l.level-1] == nil {
		l.level--
	}
}

// RankOf returns the 1-based unique rank.
func (l *List) RankOf(pid uint64) (int, bool) {
	k, ok := l.keys[pid]
	if !ok {
		return 0, false
	}
	x := l.head
	rank := 0
	for i := l.level - 1; i >= 0; i-- {
		for x.next[i] != nil && bytes.Compare(x.next[i].key[:], k[:]) < 0 {
			rank += x.span[i]
			x = x.next[i]
		}
	}
	if x.next[0] == nil || x.next[0].key != k {
		return 0, false
	}
	return rank + 1, true
}

// TopK returns at most k leading entries.
func (l *List) TopK(k int) []Entry {
	if k < 0 {
		k = 0
	}
	if k > l.length {
		k = l.length
	}
	out := make([]Entry, 0, k)
	x := l.head.next[0]
	for i := 0; i < k && x != nil; i++ {
		out = append(out, decodeKey(x.key))
		x = x.next[0]
	}
	return out
}

// Len reports entries.
func (l *List) Len() int { return l.length }

// KeyOf exposes the encoded key for an entry.
func (l *List) KeyOf(e Entry) Key { return l.order.Key(e) }

func (l *List) randomLevel() int {
	lvl := 1
	for lvl < maxLevel && l.rnd.Float64() < levelP {
		lvl++
	}
	return lvl
}

func decodeKey(k Key) Entry {
	power := int64(^binary.BigEndian.Uint64(k[0:8]))
	tie := int64(binary.BigEndian.Uint64(k[8:16]) ^ (1 << 63))
	pid := binary.BigEndian.Uint64(k[16:24])
	return Entry{PID: pid, Power: power, Tie: tie}
}

func targetKeyPID(k Key) uint64 { return binary.BigEndian.Uint64(k[16:24]) }
