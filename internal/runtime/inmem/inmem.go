// Package inmem is a single-process ActorSystem used by unit tests and by
// local runs without goakt. It is intentionally minimal: no clustering, no
// persistence, no delivery guarantees beyond a bounded channel.
package inmem

import (
	"context"
	"hash/fnv"
	"sync"
	"time"

	"hapeserver/internal/actor"
)

type box struct {
	a    actor.Actor
	ch   chan *actor.Envelope
	done chan struct{}
}

// System implements actor.System in memory.
type System struct {
	mu      sync.RWMutex
	started bool
	boxes   map[actor.Ref]*box
	nextID  uint64
}

// New returns an empty in-memory system.
func New() *System {
	return &System{boxes: make(map[actor.Ref]*box)}
}

func (s *System) Start(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.started = true
	return nil
}

func (s *System) Stop(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for ref, b := range s.boxes {
		close(b.done)
		delete(s.boxes, ref)
	}
	s.started = false
	return nil
}

func (s *System) Spawn(_ context.Context, id string, a actor.Actor) (actor.Ref, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started {
		return actor.Ref{}, actor.ErrNotStarted
	}
	s.nextID++
	ref := actor.Ref{ID: hashID(id)}
	b := &box{a: a, ch: make(chan *actor.Envelope, 256), done: make(chan struct{})}
	s.boxes[ref] = b
	go s.loop(ref, b)
	return ref, nil
}

func (s *System) loop(ref actor.Ref, b *box) {
	ctx := &contextImpl{sys: s, self: ref}
	for {
		select {
		case <-b.done:
			return
		case env := <-b.ch:
			b.a.Receive(ctx, env)
		}
	}
}

func (s *System) Tell(_ context.Context, dst actor.Ref, env *actor.Envelope) error {
	s.mu.RLock()
	b := s.boxes[dst]
	s.mu.RUnlock()
	if b == nil {
		return actor.ErrNotFound
	}
	select {
	case b.ch <- env:
		return nil
	default:
		return actor.ErrBusy
	}
}

func (s *System) Ask(context.Context, actor.Ref, *actor.Envelope, time.Duration) (*actor.Envelope, error) {
	return nil, actor.ErrUnsupported
}

type contextImpl struct {
	sys  *System
	self actor.Ref
}

func (c *contextImpl) Self() actor.Ref { return c.self }

func (c *contextImpl) Tell(dst actor.Ref, env *actor.Envelope) error {
	return c.sys.Tell(context.Background(), dst, env)
}

func (c *contextImpl) Ask(context.Context, actor.Ref, *actor.Envelope, time.Duration) (*actor.Envelope, error) {
	return nil, actor.ErrUnsupported
}

func (c *contextImpl) Schedule(d time.Duration, env *actor.Envelope) actor.TimerID {
	env.Src = c.self
	time.AfterFunc(d, func() { _ = c.sys.Tell(context.Background(), env.Dst, env) })
	return 0
}

func (c *contextImpl) Cancel(actor.TimerID) {}

func (c *contextImpl) Now() time.Time { return time.Now() }

func hashID(id string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(id))
	return h.Sum64()
}
