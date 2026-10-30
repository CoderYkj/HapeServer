// Package goakt adapts github.com/tochemey/goakt/v4 to the actor package.
// Business actors never import goakt directly.
package goakt

import (
	"context"
	"fmt"
	"hash/fnv"
	"sync"
	"time"

	goakt "github.com/tochemey/goakt/v4/actor"

	"hapeserver/internal/actor"
)

// System wraps one goakt ActorSystem.
type System struct {
	start func(context.Context) error
	stop  func(context.Context) error
	spawn func(context.Context, string, goakt.Actor) (*goakt.PID, error)

	mu     sync.RWMutex
	refs   map[actor.Ref]*goakt.PID
	timers map[actor.TimerID]*time.Timer
	nextID uint64
}

// New builds a standalone goakt system (no remoting or cluster in P0).
func New() (actor.System, error) {
	sys, err := goakt.NewActorSystem("hapeserver")
	if err != nil {
		return nil, fmt.Errorf("new goakt system: %w", err)
	}
	s := &System{
		start:  sys.Start,
		stop:   sys.Stop,
		refs:   make(map[actor.Ref]*goakt.PID),
		timers: make(map[actor.TimerID]*time.Timer),
	}
	s.spawn = func(ctx context.Context, name string, a goakt.Actor) (*goakt.PID, error) {
		return sys.Spawn(ctx, name, a)
	}
	return s, nil
}

func (s *System) Start(ctx context.Context) error { return s.start(ctx) }
func (s *System) Stop(ctx context.Context) error  { return s.stop(ctx) }

func (s *System) Spawn(ctx context.Context, id string, a actor.Actor) (actor.Ref, error) {
	ref := actor.Ref{ID: hashID(id)}
	wrapped := &goaktActor{sys: s, ref: ref, inner: a}
	pid, err := s.spawn(ctx, id, wrapped)
	if err != nil {
		return actor.Ref{}, err
	}
	s.mu.Lock()
	s.refs[ref] = pid
	s.mu.Unlock()
	return ref, nil
}

func (s *System) Tell(ctx context.Context, dst actor.Ref, env *actor.Envelope) error {
	s.mu.RLock()
	pid := s.refs[dst]
	s.mu.RUnlock()
	if pid == nil {
		return actor.ErrNotFound
	}
	return goakt.Tell(ctx, pid, env)
}

func (s *System) Ask(ctx context.Context, dst actor.Ref, env *actor.Envelope, timeout time.Duration) (*actor.Envelope, error) {
	s.mu.RLock()
	pid := s.refs[dst]
	s.mu.RUnlock()
	if pid == nil {
		return nil, actor.ErrNotFound
	}
	reply, err := goakt.Ask(ctx, pid, env, timeout)
	if err != nil {
		return nil, err
	}
	out, ok := reply.(*actor.Envelope)
	if !ok {
		return nil, fmt.Errorf("unexpected reply type %T", reply)
	}
	return out, nil
}

type goaktActor struct {
	sys   *System
	ref   actor.Ref
	inner actor.Actor
}

func (g *goaktActor) PreStart(*goakt.Context) error { return nil }
func (g *goaktActor) PostStop(*goakt.Context) error { return nil }

func (g *goaktActor) Receive(rc *goakt.ReceiveContext) {
	env, ok := rc.Message().(*actor.Envelope)
	if !ok {
		rc.Unhandled()
		return
	}
	g.inner.Receive(&contextImpl{sys: g.sys, self: g.ref, rc: rc}, env)
}

type contextImpl struct {
	sys  *System
	self actor.Ref
	rc   *goakt.ReceiveContext
}

func (c *contextImpl) Self() actor.Ref { return c.self }

func (c *contextImpl) Tell(dst actor.Ref, env *actor.Envelope) error {
	return c.sys.Tell(context.Background(), dst, env)
}

func (c *contextImpl) Ask(ctx context.Context, dst actor.Ref, env *actor.Envelope, timeout time.Duration) (*actor.Envelope, error) {
	return c.sys.Ask(ctx, dst, env, timeout)
}

func (c *contextImpl) Schedule(d time.Duration, env *actor.Envelope) actor.TimerID {
	c.sys.mu.Lock()
	c.sys.nextID++
	id := actor.TimerID(c.sys.nextID)
	env.Src = c.self
	c.sys.timers[id] = time.AfterFunc(d, func() {
		_ = c.sys.Tell(context.Background(), env.Dst, env)
	})
	c.sys.mu.Unlock()
	return id
}

func (c *contextImpl) Cancel(id actor.TimerID) {
	c.sys.mu.Lock()
	if t := c.sys.timers[id]; t != nil {
		t.Stop()
		delete(c.sys.timers, id)
	}
	c.sys.mu.Unlock()
}

func (c *contextImpl) Now() time.Time { return time.Now() }

func (c *contextImpl) Respond(env *actor.Envelope) error {
	if c.rc == nil {
		return actor.ErrUnsupported
	}
	c.rc.Response(env)
	return nil
}

func hashID(id string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(id))
	return h.Sum64()
}
