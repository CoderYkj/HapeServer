// Package world contains the P0 player and map-region actors. Gameplay is
// intentionally minimal; the goal is to exercise the runtime contracts.
package world

import (
	"encoding/json"
	"time"

	"hapeserver/internal/actor"
)

// Assets is the subset of player resources P0 exercises.
type Assets struct {
	Gold int64 `json:"gold"`
	Wood int64 `json:"wood"`
	Food int64 `json:"food"`
}

// March is a troop movement with an arrival deadline.
type March struct {
	ID       uint64 `json:"id"`
	From     uint64 `json:"from"`
	To       uint64 `json:"to"`
	ArriveAt int64  `json:"arriveAt"`
}

// Queue is one building/research slot.
type Queue struct {
	ID       uint64 `json:"id"`
	Kind     string `json:"kind"`
	DoneAt   int64  `json:"doneAt"`
	Canceled bool   `json:"canceled"`
}

// CityState is the minimal city model.
type CityState struct {
	Level int `json:"level"`
	X     int `json:"x"`
	Y     int `json:"y"`
}

// PlayerState is the actor's authoritative state.
type PlayerState struct {
	PlayerID  uint64            `json:"playerId"`
	SeasonID  uint32            `json:"seasonId"`
	Resources Assets            `json:"resources"`
	City      CityState         `json:"city"`
	Power     int64             `json:"power"`
	Marches   []March           `json:"marches"`
	Queues    []Queue           `json:"queues"`
	Escrow    map[string]Assets `json:"escrow"`
	Settled   map[string]bool   `json:"settledTrades"`
	SeenOps   []string          `json:"seenOps"`
	Version   uint64            `json:"version"`
}

// PlayerActor owns one player aggregate.
type PlayerActor struct {
	State PlayerState
}

// NewPlayer builds an empty aggregate.
func NewPlayer(id uint64, season uint32) *PlayerActor {
	return &PlayerActor{State: PlayerState{
		PlayerID: id,
		SeasonID: season,
		Escrow:   make(map[string]Assets),
		Settled:  make(map[string]bool),
	}}
}

func (p *PlayerActor) Receive(ctx actor.Context, env *actor.Envelope) {
	switch env.Kind {
	case actor.KindPing:
		p.State.Version++
	case actor.KindCommand:
		var cmd struct {
			Op      string `json:"op"`
			Gold    int64  `json:"gold"`
			Power   int64  `json:"power"`
			TradeID string `json:"tradeId"`
			Assets  Assets `json:"assets"`
		}
		if err := json.Unmarshal(env.Payload, &cmd); err != nil {
			return
		}
		if p.seen(env.OpID) {
			return
		}
		p.State.SeenOps = append(p.State.SeenOps, env.OpID)
		resp := map[string]any{"ok": true}
		switch cmd.Op {
		case "grant":
			p.State.Resources.Gold += cmd.Gold
		case "setpower":
			p.State.Power = cmd.Power
			resp["power"] = p.State.Power
		case "lock":
			if err := p.LockEscrow(cmd.TradeID, cmd.Assets); err != nil {
				resp["ok"] = false
				resp["error"] = err.Error()
			}
		case "refund":
			if err := p.RefundEscrow(cmd.TradeID); err != nil {
				resp["ok"] = false
				resp["error"] = err.Error()
			}
		case "settle":
			if err := p.SettleEscrow(cmd.TradeID, cmd.Assets); err != nil {
				resp["ok"] = false
				resp["error"] = err.Error()
			}
		}
		p.State.Version++
		b, _ := json.Marshal(resp)
		_ = ctx.Respond(&actor.Envelope{Kind: actor.KindPong, Payload: b})
	}
}

func (p *PlayerActor) seen(opID string) bool {
	if opID == "" {
		return false
	}
	for _, id := range p.State.SeenOps {
		if id == opID {
			return true
		}
	}
	return false
}

// RegionActor owns a 64x64 tile map and its reservations.
type RegionActor struct {
	WorldID  uint32
	RX, RY   uint8
	Tiles    []uint32
	Reserved map[uint64]int64 // player -> expiry unix ms
}

// NewRegion allocates an empty 64x64 region.
func NewRegion(worldID uint32, rx, ry uint8) *RegionActor {
	return &RegionActor{
		WorldID:  worldID,
		RX:       rx,
		RY:       ry,
		Tiles:    make([]uint32, 64*64),
		Reserved: make(map[uint64]int64),
	}
}

func (r *RegionActor) Receive(_ actor.Context, env *actor.Envelope) {
	switch env.Kind {
	case actor.KindCommand:
		var cmd struct {
			Op       string `json:"op"`
			PlayerID uint64 `json:"playerId"`
			Tile     int    `json:"tile"`
			TTLMS    int64  `json:"ttlMs"`
		}
		if err := json.Unmarshal(env.Payload, &cmd); err != nil {
			return
		}
		switch cmd.Op {
		case "reserve":
			if cmd.Tile >= 0 && cmd.Tile < len(r.Tiles) {
				r.Reserved[cmd.PlayerID] = time.Now().UnixMilli() + cmd.TTLMS
			}
		case "release":
			delete(r.Reserved, cmd.PlayerID)
		}
	}
}
