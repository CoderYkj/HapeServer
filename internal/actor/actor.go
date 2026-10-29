// Package actor defines the runtime abstraction shared by every business
// actor. Business code must depend on this package only, never on goakt.
package actor

import (
	"context"
	"errors"
	"time"
)

// Ref identifies an actor inside one world. Shard is 0 in single-node P0.
type Ref struct {
	Shard uint32
	ID    uint64
}

// Kind tags an envelope so receivers can dispatch without reflection.
type Kind uint16

const (
	KindPing Kind = iota + 1
	KindPong
	KindCommand
	KindEvent
)

// Envelope is the only message shape crossing actor boundaries.
type Envelope struct {
	MsgID    string
	OpID     string
	WorldID  uint32
	Src      Ref
	Dst      Ref
	Kind     Kind
	Epoch    uint32
	Deadline int64
	Payload  []byte
}

// Actor processes one envelope at a time.
type Actor interface {
	Receive(ctx Context, env *Envelope)
}

// TimerID is returned by Context.Schedule.
type TimerID uint64

// Context is the actor-facing runtime surface.
type Context interface {
	Self() Ref
	Tell(dst Ref, env *Envelope) error
	Ask(ctx context.Context, dst Ref, env *Envelope, timeout time.Duration) (*Envelope, error)
	Schedule(d time.Duration, env *Envelope) TimerID
	Cancel(id TimerID)
	Now() time.Time
}

// System spawns and routes actors.
type System interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Spawn(ctx context.Context, id string, a Actor) (Ref, error)
	Tell(ctx context.Context, dst Ref, env *Envelope) error
	Ask(ctx context.Context, dst Ref, env *Envelope, timeout time.Duration) (*Envelope, error)
}

var (
	ErrNotStarted  = errors.New("actor system not started")
	ErrNotFound    = errors.New("actor not found")
	ErrUnsupported = errors.New("operation unsupported")
	ErrTimeout     = errors.New("ask timeout")
	ErrBusy        = errors.New("mailbox full")
)
