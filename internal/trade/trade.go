// Package trade implements the directed-trade escrow state machine. Asset
// movement is performed by PlayerActor; this package owns the legal
// transitions and the reconciler contract.
package trade

import (
	"errors"
	"fmt"
)

// State is the lifecycle of a trade document.
type State string

const (
	ALocked    State = "a_locked"
	BothLocked State = "both_locked"
	Settling   State = "settling"
	Settled    State = "settled"
	Refunding  State = "refunding"
	Refunded   State = "refunded"
)

// Assets mirrors the player escrow payload.
type Assets struct {
	Gold  int64 `json:"gold"`
	Wood  int64 `json:"wood"`
	Food  int64 `json:"food"`
	Items []int `json:"items"`
}

// Trade is the authoritative document stored in MongoDB.
type Trade struct {
	ID       string `json:"_id" bson:"_id"`
	World    uint32 `json:"world" bson:"world"`
	Season   uint32 `json:"season" bson:"season"`
	A        uint64 `json:"a" bson:"a"`
	B        uint64 `json:"b" bson:"b"`
	AAssets  Assets `json:"aAssets" bson:"aAssets"`
	BAssets  Assets `json:"bAssets" bson:"bAssets"`
	State    State  `json:"state" bson:"state"`
	Version  uint64 `json:"version" bson:"version"`
	ExpireAt int64  `json:"expireAt" bson:"expireAt"`
}

// ErrIllegalTransition is returned for a move not in the state machine.
var ErrIllegalTransition = errors.New("illegal trade transition")

var transitions = map[State]map[State]bool{
	ALocked:    {BothLocked: true, Refunding: true},
	BothLocked: {Settling: true, Refunding: true},
	Settling:   {Settled: true, Refunding: true},
	Refunding:  {Refunded: true},
}

// Transition applies a state change with a version check.
func (t *Trade) Transition(to State, version uint64) error {
	if version != t.Version+1 {
		return fmt.Errorf("trade %s: stale version %d (want %d)", t.ID, version, t.Version+1)
	}
	if !transitions[t.State][to] {
		return fmt.Errorf("%w: %s -> %s", ErrIllegalTransition, t.State, to)
	}
	t.State = to
	t.Version = version
	return nil
}

// CanSettle reports whether both sides locked assets.
func (t *Trade) CanSettle() bool { return t.State == BothLocked }
