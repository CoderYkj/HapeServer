package world

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"

	"hapeserver/internal/actor"
	"hapeserver/internal/battle"
	"hapeserver/internal/observability"

	"hapegateway/protocol/worldv1"
	"hapeserver/internal/rank"
	"hapeserver/internal/trade"
	"hapeserver/internal/wal"
)

// Record is one durable player-side effect. It is appended to the WAL before
// the effect is applied, so a replay reproduces the same state.
// RewardTier pays Gold for a final rank range (Max=0 means unbounded).
type RewardTier struct {
	MinRank int
	MaxRank int
	Gold    int64
}

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

	mutMu sync.RWMutex

	mu        sync.Mutex
	players   map[uint64]*PlayerActor
	refs      map[uint64]actor.Ref
	powers    map[uint64]uint64
	stateHist map[uint64][]PlayerState
	spawning  map[uint64]chan struct{}
	seq       uint64

	season  uint32
	rank    *rank.WorldRankActor
	trade   *trade.Service
	battles *battle.Service
	metrics *observability.Metrics

	reconcileErrs []string
	rewards       []RewardTier
	alertHook     func([]string)
}

// NewService wires the P0 subsystems together.
func NewService(sys actor.System, store trade.Store, log wal.Log) *Service {
	if log == nil {
		log = wal.NewMemory()
	}
	s := &Service{
		sys:       sys,
		wal:       log,
		players:   make(map[uint64]*PlayerActor),
		refs:      make(map[uint64]actor.Ref),
		powers:    make(map[uint64]uint64),
		stateHist: make(map[uint64][]PlayerState),
		spawning:  make(map[uint64]chan struct{}),
		season:    1,
		rank:      rank.NewWorldRankActor(nil),
		battles:   battle.NewService(nil),
		metrics:   observability.New(),
		rewards:   DefaultRewards(),
	}
	s.trade = trade.NewService(store, &escrowAdapter{svc: s})
	return s
}

// Rank exposes the leaderboard for read-only queries.
func (s *Service) Rank() *rank.WorldRankActor { return s.rank }

// SetAlertHook installs a callback for reconciliation failures.
func (s *Service) SetAlertHook(h func([]string)) { s.alertHook = h }

// SetMetrics swaps the process metrics sink.
func (s *Service) SetMetrics(m *observability.Metrics) {
	if m != nil {
		s.metrics = m
	}
}

// Metrics returns the process metrics.
func (s *Service) Metrics() *observability.Metrics { return s.metrics }

// UseBattleStore replaces the in-memory battle store with a durable one.
func (s *Service) UseBattleStore(store battle.Store) {
	if store == nil {
		return
	}
	s.mu.Lock()
	s.battles = battle.NewService(store)
	s.mu.Unlock()
}

// EnsurePlayer spawns the player aggregate on first use.
func (s *Service) EnsurePlayer(ctx context.Context, id uint64) (actor.Ref, error) {
	s.mu.Lock()
	if ref, ok := s.refs[id]; ok {
		s.mu.Unlock()
		return ref, nil
	}
	if ch, ok := s.spawning[id]; ok {
		s.mu.Unlock()
		<-ch
		s.mu.Lock()
		ref, ok := s.refs[id]
		s.mu.Unlock()
		if !ok {
			return actor.Ref{}, fmt.Errorf("spawn failed for player %d", id)
		}
		return ref, nil
	}
	ch := make(chan struct{})
	s.spawning[id] = ch
	season := s.season
	s.mu.Unlock()

	p := NewPlayer(id, season)
	p.State.Resources = Assets{Gold: 1000, Wood: 1000, Food: 1000}
	ref, err := s.sys.Spawn(ctx, fmt.Sprintf("player-%d", id), p)

	s.mu.Lock()
	delete(s.spawning, id)
	if err != nil {
		close(ch)
		s.mu.Unlock()
		return actor.Ref{}, err
	}
	s.players[id] = p
	s.refs[id] = ref
	s.stateHist[id] = []PlayerState{cloneState(p.State)}
	s.rank.Update(rank.Update{PID: id, Power: 0, Version: 1})
	s.powers[id] = 1
	close(ch)
	s.mu.Unlock()
	return ref, nil
}

// Prewarm creates players 1..n before serving traffic.
func (s *Service) Prewarm(ctx context.Context, n uint64) error {
	sem := make(chan struct{}, 64)
	var wg sync.WaitGroup
	var first error
	var mu sync.Mutex
	for id := uint64(1); id <= n; id++ {
		wg.Add(1)
		sem <- struct{}{}
		go func(id uint64) {
			defer wg.Done()
			defer func() { <-sem }()
			if _, err := s.EnsurePlayer(ctx, id); err != nil {
				mu.Lock()
				if first == nil {
					first = err
				}
				mu.Unlock()
			}
		}(id)
	}
	wg.Wait()
	return first
}

// SetSeason changes the current season used for new and stale players.
func (s *Service) SetSeason(season uint32) {
	s.mu.Lock()
	s.season = season
	s.mu.Unlock()
}

// CurrentSeason reports the active season.
func (s *Service) CurrentSeason() uint32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.season
}

func (s *Service) resetPlayer(ctx context.Context, pid uint64, ref actor.Ref, season uint32) (bool, error) {
	s.mu.Lock()
	p := s.players[pid]
	if p == nil || p.State.SeasonID == season {
		s.mu.Unlock()
		return false, nil
	}
	if len(p.State.Escrow) > 0 {
		s.mu.Unlock()
		return false, ErrActiveEscrow
	}
	s.mu.Unlock()

	if _, err := s.ask(ctx, pid, ref, map[string]any{"op": "reset", "season": season}); err != nil {
		return false, err
	}
	s.mu.Lock()
	s.powers[pid]++
	v := s.powers[pid]
	s.mu.Unlock()
	s.rank.Update(rank.Update{PID: pid, Power: 0, Version: v})
	return true, nil
}

// SweepSeason lazily resets every known player whose season is stale.
// Players with active escrow are skipped and counted separately.
func (s *Service) SweepSeason(ctx context.Context, season uint32) (int, int) {
	s.SetSeason(season)
	s.mu.Lock()
	ids := make([]uint64, 0, len(s.players))
	for id := range s.players {
		ids = append(ids, id)
	}
	s.mu.Unlock()

	reset, skipped := 0, 0
	for _, id := range ids {
		ref, err := s.EnsurePlayer(ctx, id)
		if err != nil {
			continue
		}
		ok, err := s.resetPlayer(ctx, id, ref, season)
		switch {
		case errors.Is(err, ErrActiveEscrow):
			skipped++
		case err == nil && ok:
			reset++
		}
	}
	return reset, skipped
}

// DefaultRewards mirrors the P0 season table.
func DefaultRewards() []RewardTier {
	return []RewardTier{
		{MinRank: 1, MaxRank: 1, Gold: 5000},
		{MinRank: 2, MaxRank: 10, Gold: 2000},
		{MinRank: 11, MaxRank: 100, Gold: 1000},
		{MinRank: 101, MaxRank: 1000, Gold: 500},
		{MinRank: 1001, MaxRank: 0, Gold: 100},
	}
}

// SetRewards replaces the season reward table.
func (s *Service) SetRewards(tiers []RewardTier) {
	if len(tiers) > 0 {
		s.rewards = tiers
	}
}

// seasonReward is the default gold paid for a final rank.
func seasonReward(rank int) int64 {
	return rewardForRank(DefaultRewards(), rank)
}

func rewardForRank(tiers []RewardTier, rank int) int64 {
	for _, t := range tiers {
		if rank >= t.MinRank && (t.MaxRank == 0 || rank <= t.MaxRank) {
			return t.Gold
		}
	}
	return 0
}

func (s *Service) rewardForRank(rank int) int64 { return rewardForRank(s.rewards, rank) }

// SettleSeason resets stale players and pays rank rewards for the finished
// season. It must be called before the new season's players are admitted.
func (s *Service) SettleSeason(ctx context.Context, season uint32) (reset, rewarded, skipped int) {
	tops := s.rank.TopK(10000)
	rewards := make(map[uint64]int64, len(tops))
	for i := range tops {
		if tops[i].Power <= 0 {
			continue
		}
		rewards[tops[i].PID] = s.rewardForRank(i + 1)
	}

	s.SetSeason(season)
	s.mu.Lock()
	ids := make([]uint64, 0, len(s.players))
	for id := range s.players {
		ids = append(ids, id)
	}
	s.mu.Unlock()

	for _, id := range ids {
		ref, err := s.EnsurePlayer(ctx, id)
		if err != nil {
			continue
		}
		ok, err := s.resetPlayer(ctx, id, ref, season)
		if errors.Is(err, ErrActiveEscrow) {
			skipped++
			continue
		}
		if err != nil || !ok {
			continue
		}
		reset++
		if r := rewards[id]; r > 0 {
			if err := s.Grant(ctx, id, r); err == nil {
				rewarded++
			}
		}
	}
	return reset, rewarded, skipped
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
	s.mu.Lock()
	season := s.season
	s.mu.Unlock()
	return s.trade.Create(ctx, 1, season, a, b, trade.Assets{Gold: aGold}, trade.Assets{Gold: bGold}, 10*time.Minute)
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

func (s *Service) snapshotStates() []PlayerState {
	s.mu.Lock()
	defer s.mu.Unlock()
	states := make([]PlayerState, 0, len(s.players))
	for _, p := range s.players {
		states = append(states, cloneState(p.State))
	}
	return states
}

// Snapshot serializes all live player states. JSON encoding happens outside
// the state lock so reads are not blocked by a large snapshot.
func (s *Service) Snapshot() ([]byte, error) {
	return encodeStates(s.snapshotStates())
}

// SnapshotWithOffset captures state and the matching WAL offset while no
// mutation is in flight, so replay never double-applies or skips a record.
// The expensive JSON encoding runs after the mutation barrier is released.
func (s *Service) SnapshotWithOffset() ([]byte, uint64, error) {
	s.mutMu.Lock()
	states := s.snapshotStates()
	offset := s.wal.LastOffset()
	s.mutMu.Unlock()
	payload, err := encodeStates(states)
	if err != nil {
		return nil, 0, err
	}
	return payload, offset, nil
}

func readOnlyAction(action string) bool {
	switch action {
	case "state", "rank", "top", "battle_get", "battle_history", "trade_get", "reconcile", "state_delta":
		return true
	default:
		return false
	}
}

// Restore recreates player actors from a snapshot payload.
func (s *Service) Restore(ctx context.Context, payload []byte) error {
	states, err := decodeStates(payload)
	if err != nil {
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
		s.stateHist[st.PlayerID] = []PlayerState{cloneState(st)}
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
	Seq         uint64 `json:"seq"`
	PlayerID    uint64 `json:"playerId"`
	Target      uint64 `json:"targetId"`
	Action      string `json:"action"`
	Gold        int64  `json:"gold"`
	TargetGold  int64  `json:"targetGold"`
	Power       int64  `json:"power"`
	TradeID     string `json:"tradeId"`
	K           int    `json:"k"`
	Season      uint32 `json:"season"`
	DefPower    int64  `json:"defPower"`
	BattleID    string `json:"battleId"`
	FromVersion uint64 `json:"fromVersion"`
	Before      int64  `json:"before"`
	BeforeID    string `json:"beforeId"`
}

// Handle dispatches one API action and returns a typed protobuf response.
func (s *Service) Handle(ctx context.Context, req Request) (*worldv1.Response, error) {
	s.metrics.Commands.Add(1)
	if !readOnlyAction(req.Action) {
		s.mutMu.RLock()
		defer s.mutMu.RUnlock()
	}
	data, err := s.handle(ctx, req)
	resp := &worldv1.Response{Ok: err == nil}
	if err != nil {
		s.metrics.Errors.Add(1)
		resp.Error = err.Error()
	} else {
		encodeResult(resp, req.Action, data)
		if !readOnlyAction(req.Action) {
			s.captureState(req.PlayerID)
		}
	}
	return resp, err
}

func (s *Service) handle(ctx context.Context, req Request) (any, error) {
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
		s.metrics.RankLookup.Add(1)
		rankN, ok := s.rank.RankOf(req.PlayerID)
		return map[string]any{"rank": rankN, "ok": ok}, nil
	case "top":
		s.metrics.RankLookup.Add(1)
		k := req.K
		if k <= 0 {
			k = 10
		}
		return s.rank.TopK(k), nil
	case "trade_create":
		data, err := s.TradeCreate(ctx, req.PlayerID, req.Target, req.Gold, req.TargetGold)
		return s.tradeResult(data, err)
	case "trade_accept":
		return s.tradeResult(nil, s.TradeAccept(ctx, req.TradeID, req.PlayerID))
	case "trade_settle":
		return s.tradeResult(nil, s.TradeSettle(ctx, req.TradeID))
	case "trade_cancel":
		return s.tradeResult(nil, s.TradeCancel(ctx, req.TradeID))
	case "trade_get":
		return s.trade.Get(ctx, req.TradeID)
	case "state_delta":
		d := s.Delta(req.PlayerID, req.FromVersion)
		if d == nil {
			return nil, fmt.Errorf("state version unavailable")
		}
		return d, nil
	case "reconcile":
		n, err := s.Reconcile(ctx, int64(req.K))
		return map[string]any{"touched": n}, err
	case "battle_submit":
		return s.battles.Submit(ctx, 1, s.CurrentSeason(), req.PlayerID, req.Target, req.Power, req.DefPower)
	case "battle_get":
		return s.battles.Get(ctx, req.BattleID)
	case "battle_history":
		limit := int64(req.K)
		if limit <= 0 {
			limit = 20
		}
		return s.battles.History(ctx, req.PlayerID, req.Season, req.Before, req.BeforeID, limit)
	case "season_reset":
		season := req.Season
		if season == 0 {
			season = s.CurrentSeason() + 1
		}
		reset, rewarded, skipped := s.SettleSeason(ctx, season)
		return map[string]any{"season": season, "reset": reset, "rewarded": rewarded, "skipped": skipped}, nil
	default:
		return nil, fmt.Errorf("unknown action %q", req.Action)
	}
}

func (s *Service) tradeResult(data any, err error) (any, error) {
	if err != nil {
		s.metrics.TradeFail.Add(1)
	} else {
		s.metrics.TradeOK.Add(1)
	}
	return data, err
}

const stateHistoryLimit = 8

func cloneState(st PlayerState) PlayerState {
	out := st
	out.Marches = append([]March(nil), st.Marches...)
	out.Queues = append([]Queue(nil), st.Queues...)
	out.SeenOps = append([]string(nil), st.SeenOps...)
	out.Escrow = make(map[string]Assets, len(st.Escrow))
	for k, v := range st.Escrow {
		out.Escrow[k] = v
	}
	out.Settled = make(map[string]bool, len(st.Settled))
	for k, v := range st.Settled {
		out.Settled[k] = v
	}
	return out
}

func (s *Service) captureState(pid uint64) {
	if pid == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.players[pid]
	if p == nil {
		return
	}
	cur := cloneState(p.State)
	h := s.stateHist[pid]
	if len(h) > 0 && h[len(h)-1].Version == cur.Version {
		return
	}
	h = append(h, cur)
	if len(h) > stateHistoryLimit {
		h = h[len(h)-stateHistoryLimit:]
	}
	s.stateHist[pid] = h
}

// Delta builds a field-level diff from fromVersion to the current state.
// It returns nil when the base version is no longer retained.
func (s *Service) Delta(pid, fromVersion uint64) *worldv1.StateDelta {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.players[pid]
	if p == nil {
		return nil
	}
	cur := cloneState(p.State)
	var base PlayerState
	found := false
	for _, st := range s.stateHist[pid] {
		if st.Version == fromVersion {
			base = st
			found = true
			break
		}
	}
	if !found {
		return nil
	}
	d := &worldv1.StateDelta{FromVersion: fromVersion, StateVersion: cur.Version}
	if !reflect.DeepEqual(base.Resources, cur.Resources) {
		d.Changed = append(d.Changed, "resources")
		d.Resources = assetsToProto(cur.Resources)
	}
	if !reflect.DeepEqual(base.City, cur.City) {
		d.Changed = append(d.Changed, "city")
		d.City = &worldv1.City{Level: int32(cur.City.Level), X: int32(cur.City.X), Y: int32(cur.City.Y)}
	}
	if base.Power != cur.Power {
		d.Changed = append(d.Changed, "power")
		d.Power = cur.Power
	}
	if !reflect.DeepEqual(base.Marches, cur.Marches) {
		d.Changed = append(d.Changed, "marches")
		for _, m := range cur.Marches {
			d.Marches = append(d.Marches, &worldv1.March{Id: m.ID, From: m.From, To: m.To, ArriveAt: m.ArriveAt})
		}
	}
	if !reflect.DeepEqual(base.Queues, cur.Queues) {
		d.Changed = append(d.Changed, "queues")
		for _, q := range cur.Queues {
			d.Queues = append(d.Queues, &worldv1.Queue{Id: q.ID, Kind: q.Kind, DoneAt: q.DoneAt, Canceled: q.Canceled})
		}
	}
	if !reflect.DeepEqual(base.Escrow, cur.Escrow) {
		d.Changed = append(d.Changed, "escrow")
		d.Escrow = make(map[string]*worldv1.Assets, len(cur.Escrow))
		for k, v := range cur.Escrow {
			d.Escrow[k] = assetsToProto(v)
		}
	}
	if !reflect.DeepEqual(base.Settled, cur.Settled) {
		d.Changed = append(d.Changed, "settled_trades")
		d.SettledTrades = make(map[string]bool, len(cur.Settled))
		for k, v := range cur.Settled {
			d.SettledTrades[k] = v
		}
	}
	return d
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
	p := s.players[pid]
	stale := p != nil && p.State.SeasonID != s.season
	season := s.season
	s.mu.Unlock()
	if stale {
		if _, err := s.resetPlayer(ctx, pid, ref, season); err != nil {
			return nil, err
		}
	}
	return s.ask(ctx, pid, ref, body)
}

func (s *Service) ask(ctx context.Context, pid uint64, ref actor.Ref, body map[string]any) (map[string]any, error) {
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

// Reconcile resumes non-terminal trades (settling or expired).
func (s *Service) Reconcile(ctx context.Context, limit int64) (int, error) {
	s.mutMu.RLock()
	defer s.mutMu.RUnlock()
	res, err := s.trade.ReconcileDetailed(ctx, limit)
	s.metrics.ReconcileRuns.Add(1)
	if err != nil {
		s.metrics.ReconcileErrors.Add(1)
		return 0, err
	}
	s.metrics.ReconcileTouched.Add(uint64(res.Touched))
	s.metrics.SetGauge("reconcile_last_touched", int64(res.Touched))
	s.metrics.ReconcileErrors.Add(uint64(len(res.Errors)))
	s.metrics.SetGauge("reconcile_last_error_count", int64(len(res.Errors)))
	if len(res.Errors) > 0 {
		s.mu.Lock()
		s.reconcileErrs = append(s.reconcileErrs, res.Errors...)
		if len(s.reconcileErrs) > 20 {
			s.reconcileErrs = s.reconcileErrs[len(s.reconcileErrs)-20:]
		}
		s.mu.Unlock()
		s.metrics.ReconcileAlerts.Add(1)
		if s.alertHook != nil {
			s.alertHook(append([]string(nil), res.Errors...))
		}
	}
	return res.Touched, nil
}

// ReconcileErrors returns the last reconciliation failures (newest last).
func (s *Service) ReconcileErrors() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.reconcileErrs))
	copy(out, s.reconcileErrs)
	return out
}
