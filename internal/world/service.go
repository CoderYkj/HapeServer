package world

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"hapeserver/internal/actor"
	"hapeserver/internal/rank"
	"hapeserver/internal/trade"
	"hapeserver/internal/wal"
)

// Record is one durable player-side effect. It is appended to the WAL before
// the effect is applied, so a replay reproduces the same state.
type Record struct {
	PlayerID uint64 `json:"playerId"`
	Op       string `json:"op"`
	Gold     int64  `json:"gold"`
	Power    int64  `json:"power"`
	TradeID  string `json:"tradeId,omitempty"`
	Assets   Assets `json:"assets,omitempty"`
}

// Service is the single-node P0 facade used by the HTTP gateway and bots.
type Service struct {
	sys actor.System
	wal wal.Log

	mu      sync.Mutex
	players map[uint64]*PlayerActor
	refs    map[uint64]actor.Ref
	powers  map[uint64]uint64
	seq     uint64

	rank  *rank.WorldRankActor
	trade *trade.Service
}

// NewService wires the P0 subsystems together.
func NewService(sys actor.System, store trade.Store, log wal.Log) *Service {
	if log == nil {
		log = wal.NewMemory()
	}
	s := &Service{
		sys:     sys,
		wal:     log,
		players: make(map[uint64]*PlayerActor),
		refs:    make(map[uint64]actor.Ref),
		powers:  make(map[uint64]uint64),
		rank:    rank.NewWorldRankActor(nil),
	}
	s.trade = trade.NewService(store, &escrowAdapter{svc: s})
	return s
}

// Rank exposes the leaderboard for read-only queries.
func (s *Service) Rank() *rank.WorldRankActor { return s.rank }

// EnsurePlayer spawns the player aggregate on first use.
func (s *Service) EnsurePlayer(ctx context.Context, id uint64) (actor.Ref, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ref, ok := s.refs[id]; ok {
		return ref, nil
	}
	p := NewPlayer(id, 1)
	p.State.Resources = Assets{Gold: 1000, Wood: 1000, Food: 1000}
	ref, err := s.sys.Spawn(ctx, fmt.Sprintf("player-%d", id), p)
	if err != nil {
		return actor.Ref{}, err
	}
	s.players[id] = p
	s.refs[id] = ref
	s.rank.Update(rank.Update{PID: id, Power: 0, Version: 1})
	s.powers[id] = 1
	return ref, nil
}

// SetPower updates the player's power and the leaderboard.
func (s *Service) SetPower(ctx context.Context, id uint64, power int64) error {
	if err := s.record(Record{PlayerID: id, Op: "setpower", Power: power}); err != nil {
		return err
	}
	return s.applyPower(ctx, id, power)
}

// Grant adds gold; used by bots and dev tools.
func (s *Service) Grant(ctx context.Context, id uint64, gold int64) error {
	if err := s.record(Record{PlayerID: id, Op: "grant", Gold: gold}); err != nil {
		return err
	}
	_, err := s.dispatch(ctx, id, map[string]any{"op": "grant", "gold": gold})
	return err
}

// TradeCreate locks A's assets and creates the trade document.
func (s *Service) TradeCreate(ctx context.Context, a, b uint64, aGold, bGold int64) (*trade.Trade, error) {
	if _, err := s.EnsurePlayer(ctx, a); err != nil {
		return nil, err
	}
	if _, err := s.EnsurePlayer(ctx, b); err != nil {
		return nil, err
	}
	return s.trade.Create(ctx, 1, 1, a, b, trade.Assets{Gold: aGold}, trade.Assets{Gold: bGold}, 10*time.Minute)
}

// TradeAccept locks B's side.
func (s *Service) TradeAccept(ctx context.Context, tradeID string, b uint64) error {
	if _, err := s.EnsurePlayer(ctx, b); err != nil {
		return err
	}
	return s.trade.Accept(ctx, tradeID, b)
}

// TradeSettle settles both sides.
func (s *Service) TradeSettle(ctx context.Context, id string) error {
	return s.trade.Settle(ctx, id)
}

// TradeCancel refunds both sides.
func (s *Service) TradeCancel(ctx context.Context, id string) error {
	return s.trade.Cancel(ctx, id)
}

// Snapshot serializes all live player states.
func (s *Service) Snapshot() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	states := make([]PlayerState, 0, len(s.players))
	for _, p := range s.players {
		states = append(states, p.State)
	}
	return json.Marshal(states)
}

// Restore recreates player actors from a snapshot payload.
func (s *Service) Restore(ctx context.Context, payload []byte) error {
	var states []PlayerState
	if err := json.Unmarshal(payload, &states); err != nil {
		return err
	}
	for i := range states {
		st := states[i]
		if st.Escrow == nil {
			st.Escrow = make(map[string]Assets)
		}
		if st.Settled == nil {
			st.Settled = make(map[string]bool)
		}
		p := &PlayerActor{State: st}
		s.mu.Lock()
		ref, err := s.sys.Spawn(ctx, fmt.Sprintf("player-%d", st.PlayerID), p)
		if err != nil {
			s.mu.Unlock()
			return err
		}
		s.players[st.PlayerID] = p
		s.refs[st.PlayerID] = ref
		s.powers[st.PlayerID] = 1
		s.mu.Unlock()
		s.rank.Update(rank.Update{PID: st.PlayerID, Power: st.Power, Version: 1})
	}
	return nil
}

// ApplySnapshot implements recovery.Applier.
func (s *Service) ApplySnapshot(payload []byte) error {
	return s.Restore(context.Background(), payload)
}

// ApplyRecord implements recovery.Applier.
func (s *Service) ApplyRecord(_ uint64, data []byte) error {
	var r Record
	if err := json.Unmarshal(data, &r); err != nil {
		return err
	}
	ctx := context.Background()
	switch r.Op {
	case "grant":
		_, err := s.dispatch(ctx, r.PlayerID, map[string]any{"op": "grant", "gold": r.Gold})
		return err
	case "setpower":
		return s.applyPower(ctx, r.PlayerID, r.Power)
	case "lock":
		_, err := s.dispatch(ctx, r.PlayerID, map[string]any{"op": "lock", "tradeId": r.TradeID, "assets": r.Assets})
		return err
	case "refund":
		_, err := s.dispatch(ctx, r.PlayerID, map[string]any{"op": "refund", "tradeId": r.TradeID})
		return err
	case "settle":
		_, err := s.dispatch(ctx, r.PlayerID, map[string]any{"op": "settle", "tradeId": r.TradeID, "assets": r.Assets})
		return err
	default:
		return fmt.Errorf("unknown record op %q", r.Op)
	}
}

// State returns a player's current state for dev/testing.
func (s *Service) State(id uint64) (PlayerState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.players[id]
	if !ok {
		return PlayerState{}, false
	}
	return p.State, true
}

// Request is one API action.
type Request struct {
	Seq        uint64 `json:"seq"`
	PlayerID   uint64 `json:"playerId"`
	Target     uint64 `json:"targetId"`
	Action     string `json:"action"`
	Gold       int64  `json:"gold"`
	TargetGold int64  `json:"targetGold"`
	Power      int64  `json:"power"`
	TradeID    string `json:"tradeId"`
	K          int    `json:"k"`
}

// Handle dispatches one API action.
func (s *Service) Handle(ctx context.Context, req Request) (any, error) {
	switch req.Action {
	case "grant":
		return nil, s.Grant(ctx, req.PlayerID, req.Gold)
	case "setpower":
		return nil, s.SetPower(ctx, req.PlayerID, req.Power)
	case "state":
		st, ok := s.State(req.PlayerID)
		if !ok {
			return nil, fmt.Errorf("player not found")
		}
		return st, nil
	case "rank":
		rankN, ok := s.rank.RankOf(req.PlayerID)
		return map[string]any{"rank": rankN, "ok": ok}, nil
	case "top":
		k := req.K
		if k <= 0 {
			k = 10
		}
		return s.rank.TopK(k), nil
	case "trade_create":
		return s.TradeCreate(ctx, req.PlayerID, req.Target, req.Gold, req.TargetGold)
	case "trade_accept":
		return nil, s.TradeAccept(ctx, req.TradeID, req.PlayerID)
	case "trade_settle":
		return nil, s.TradeSettle(ctx, req.TradeID)
	case "trade_cancel":
		return nil, s.TradeCancel(ctx, req.TradeID)
	default:
		return nil, fmt.Errorf("unknown action %q", req.Action)
	}
}

func (s *Service) applyPower(ctx context.Context, id uint64, power int64) error {
	if _, err := s.dispatch(ctx, id, map[string]any{"op": "setpower", "power": power}); err != nil {
		return err
	}
	s.mu.Lock()
	s.powers[id]++
	v := s.powers[id]
	s.mu.Unlock()
	s.rank.Update(rank.Update{PID: id, Power: power, Version: v})
	return nil
}

func (s *Service) record(r Record) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = s.wal.Append(b)
	return err
}

func (s *Service) dispatch(ctx context.Context, pid uint64, body map[string]any) (map[string]any, error) {
	ref, err := s.EnsurePlayer(ctx, pid)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.seq++
	op := fmt.Sprintf("op-%d-%d", pid, s.seq)
	s.mu.Unlock()

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	env := &actor.Envelope{
		MsgID:   op,
		OpID:    op,
		WorldID: 1,
		Kind:    actor.KindCommand,
		Payload: payload,
	}
	resp, err := s.sys.Ask(ctx, ref, env, 3*time.Second)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(resp.Payload, &out); err != nil {
		return nil, err
	}
	if ok, exists := out["ok"].(bool); exists && !ok {
		return out, fmt.Errorf("%v", out["error"])
	}
	return out, nil
}

type escrowAdapter struct{ svc *Service }

func (e *escrowAdapter) Lock(ctx context.Context, pid uint64, tradeID string, a trade.Assets) error {
	assets := Assets{Gold: a.Gold, Wood: a.Wood, Food: a.Food}
	if err := e.svc.record(Record{PlayerID: pid, Op: "lock", TradeID: tradeID, Assets: assets}); err != nil {
		return err
	}
	_, err := e.svc.dispatch(ctx, pid, map[string]any{"op": "lock", "tradeId": tradeID, "assets": assets})
	return err
}

func (e *escrowAdapter) Refund(ctx context.Context, pid uint64, tradeID string) error {
	if err := e.svc.record(Record{PlayerID: pid, Op: "refund", TradeID: tradeID}); err != nil {
		return err
	}
	_, err := e.svc.dispatch(ctx, pid, map[string]any{"op": "refund", "tradeId": tradeID})
	return err
}

func (e *escrowAdapter) Settle(ctx context.Context, pid uint64, tradeID string, credit trade.Assets) error {
	assets := Assets{Gold: credit.Gold, Wood: credit.Wood, Food: credit.Food}
	if err := e.svc.record(Record{PlayerID: pid, Op: "settle", TradeID: tradeID, Assets: assets}); err != nil {
		return err
	}
	_, err := e.svc.dispatch(ctx, pid, map[string]any{"op": "settle", "tradeId": tradeID, "assets": assets})
	return err
}
