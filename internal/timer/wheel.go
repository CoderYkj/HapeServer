// Package timer provides a deterministic time wheel for travel, queues and
// other scheduled work. P0 uses a simple ordered map; the hierarchical wheel
// lands with task B01.
package timer

import (
	"sort"
	"sync"
	"time"
)

// ID identifies one scheduled callback.
type ID uint64

type entry struct {
	id    ID
	at    time.Time
	fn    func()
	order uint64
}

// Wheel is a virtual-clock timer wheel. Callers advance time explicitly so
// tests are deterministic; the production runtime will drive Advance from a
// 100ms ticker.
type Wheel struct {
	mu     sync.Mutex
	now    time.Time
	next   ID
	seq    uint64
	timers map[ID]entry
}

// New returns a wheel anchored at start.
func New(start time.Time) *Wheel {
	return &Wheel{now: start, timers: make(map[ID]entry)}
}

// Now returns the wheel's virtual time.
func (w *Wheel) Now() time.Time {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.now
}

// After schedules fn after d.
func (w *Wheel) After(d time.Duration, fn func()) ID {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.next++
	w.seq++
	id := w.next
	w.timers[id] = entry{id: id, at: w.now.Add(d), fn: fn, order: w.seq}
	return id
}

// Cancel removes id; cancelling an unknown id is a no-op.
func (w *Wheel) Cancel(id ID) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.timers, id)
}

// Advance moves virtual time forward and fires every due timer once, in
// (deadline, insertion) order.
func (w *Wheel) Advance(d time.Duration) {
	w.mu.Lock()
	w.now = w.now.Add(d)
	due := make([]entry, 0, len(w.timers))
	for id, e := range w.timers {
		if !e.at.After(w.now) {
			due = append(due, e)
			delete(w.timers, id)
		}
	}
	now := w.now
	w.mu.Unlock()

	sort.Slice(due, func(i, j int) bool {
		if due[i].at.Equal(due[j].at) {
			return due[i].order < due[j].order
		}
		return due[i].at.Before(due[j].at)
	})
	for _, e := range due {
		e.fn()
	}
	_ = now
}

// Len reports pending timers.
func (w *Wheel) Len() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.timers)
}
