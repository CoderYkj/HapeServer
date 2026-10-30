package world

import (
	"encoding/json"

	"google.golang.org/protobuf/proto"

	"hapeserver/internal/battle"
	"hapeserver/internal/protocol/worldv1"
	"hapeserver/internal/rank"
	"hapeserver/internal/trade"
)

func encodeResult(resp *worldv1.Response, action string, data any) {
	switch action {
	case "state":
		st, _ := data.(PlayerState)
		resp.Result = &worldv1.Response_State{State: stateToProto(st)}
	case "rank":
		m, _ := data.(map[string]any)
		r, _ := m["rank"].(int)
		found, _ := m["ok"].(bool)
		resp.Result = &worldv1.Response_Rank{Rank: &worldv1.RankResult{Rank: int32(r), Found: found}}
	case "top":
		entries, _ := data.([]rank.Entry)
		top := &worldv1.TopResult{Entries: make([]*worldv1.RankEntry, 0, len(entries))}
		for _, e := range entries {
			top.Entries = append(top.Entries, &worldv1.RankEntry{Pid: e.PID, Power: e.Power})
		}
		resp.Result = &worldv1.Response_Top{Top: top}
	case "trade_create", "trade_get":
		if t, ok := data.(*trade.Trade); ok {
			resp.Result = &worldv1.Response_Trade{Trade: tradeToProto(t)}
		}
	case "battle_submit", "battle_get":
		if b, ok := data.(*battle.Battle); ok {
			resp.Result = &worldv1.Response_Battle{Battle: battleToProto(b)}
		}
	case "battle_history":
		if p, ok := data.(*battle.Page); ok {
			page := &worldv1.BattlePage{NextCreatedAt: p.NextCreatedAt, NextId: p.NextID}
			for i := range p.Items {
				page.Items = append(page.Items, battleToProto(&p.Items[i]))
			}
			resp.Result = &worldv1.Response_BattlePage{BattlePage: page}
		}
	case "state_delta":
		if d, ok := data.(*worldv1.StateDelta); ok {
			resp.Result = &worldv1.Response_Delta{Delta: d}
		}
	case "reconcile":
		m, _ := data.(map[string]any)
		n, _ := m["touched"].(int)
		resp.Result = &worldv1.Response_Stats{Stats: &worldv1.ActionStats{Touched: int32(n)}}
	case "season_reset":
		m, _ := data.(map[string]any)
		stats := &worldv1.ActionStats{}
		if v, ok := m["season"].(uint32); ok {
			stats.Season = v
		}
		if v, ok := m["reset"].(int); ok {
			stats.Reset_ = int32(v)
		}
		if v, ok := m["rewarded"].(int); ok {
			stats.Rewarded = int32(v)
		}
		if v, ok := m["skipped"].(int); ok {
			stats.Skipped = int32(v)
		}
		resp.Result = &worldv1.Response_Stats{Stats: stats}
	}
}

func stateToProto(st PlayerState) *worldv1.PlayerState {
	out := &worldv1.PlayerState{
		PlayerId:      st.PlayerID,
		SeasonId:      st.SeasonID,
		Resources:     assetsToProto(st.Resources),
		City:          &worldv1.City{Level: int32(st.City.Level), X: int32(st.City.X), Y: int32(st.City.Y)},
		Power:         st.Power,
		Escrow:        make(map[string]*worldv1.Assets, len(st.Escrow)),
		SettledTrades: make(map[string]bool, len(st.Settled)),
		SeenOps:       st.SeenOps,
		Version:       st.Version,
	}
	for _, m := range st.Marches {
		out.Marches = append(out.Marches, &worldv1.March{Id: m.ID, From: m.From, To: m.To, ArriveAt: m.ArriveAt})
	}
	for _, q := range st.Queues {
		out.Queues = append(out.Queues, &worldv1.Queue{Id: q.ID, Kind: q.Kind, DoneAt: q.DoneAt, Canceled: q.Canceled})
	}
	for k, v := range st.Escrow {
		out.Escrow[k] = assetsToProto(v)
	}
	for k, v := range st.Settled {
		out.SettledTrades[k] = v
	}
	return out
}

func assetsToProto(a Assets) *worldv1.Assets {
	return &worldv1.Assets{Gold: a.Gold, Wood: a.Wood, Food: a.Food}
}

func tradeAssetsToProto(a trade.Assets) *worldv1.Assets {
	out := &worldv1.Assets{Gold: a.Gold, Wood: a.Wood, Food: a.Food}
	for _, it := range a.Items {
		out.Items = append(out.Items, int32(it))
	}
	return out
}

func tradeToProto(t *trade.Trade) *worldv1.Trade {
	return &worldv1.Trade{
		Id:       t.ID,
		World:    t.World,
		Season:   t.Season,
		A:        t.A,
		B:        t.B,
		AAssets:  tradeAssetsToProto(t.AAssets),
		BAssets:  tradeAssetsToProto(t.BAssets),
		State:    string(t.State),
		Version:  t.Version,
		ExpireAt: t.ExpireAt,
	}
}

func battleToProto(b *battle.Battle) *worldv1.Battle {
	return &worldv1.Battle{
		Id:            b.ID,
		World:         b.World,
		Season:        b.Season,
		Attacker:      b.Attacker,
		Defender:      b.Defender,
		AttackerPower: b.AttackerPower,
		DefenderPower: b.DefenderPower,
		Winner:        b.Winner,
		State:         string(b.State),
		CreatedAt:     b.CreatedAt,
		SettledAt:     b.SettledAt,
	}
}

func encodeStates(states []PlayerState) ([]byte, error) {
	out := &worldv1.PlayerStates{Players: make([]*worldv1.PlayerState, 0, len(states))}
	for _, st := range states {
		out.Players = append(out.Players, stateToProto(st))
	}
	return proto.Marshal(out)
}

func decodeStates(payload []byte) ([]PlayerState, error) {
	if len(payload) == 0 {
		return nil, nil
	}
	var ps worldv1.PlayerStates
	if err := proto.Unmarshal(payload, &ps); err == nil && len(ps.Players) > 0 {
		states := make([]PlayerState, 0, len(ps.Players))
		for _, p := range ps.Players {
			states = append(states, stateFromProto(p))
		}
		return states, nil
	}
	var states []PlayerState
	if err := json.Unmarshal(payload, &states); err != nil {
		return nil, err
	}
	return states, nil
}

func stateFromProto(p *worldv1.PlayerState) PlayerState {
	st := PlayerState{
		PlayerID: p.PlayerId,
		SeasonID: p.SeasonId,
		Resources: Assets{
			Gold: p.GetResources().GetGold(),
			Wood: p.GetResources().GetWood(),
			Food: p.GetResources().GetFood(),
		},
		Power:   p.Power,
		Escrow:  make(map[string]Assets, len(p.Escrow)),
		Settled: make(map[string]bool, len(p.SettledTrades)),
		SeenOps: append([]string(nil), p.SeenOps...),
		Version: p.Version,
	}
	if c := p.GetCity(); c != nil {
		st.City = CityState{Level: int(c.Level), X: int(c.X), Y: int(c.Y)}
	}
	for _, m := range p.Marches {
		st.Marches = append(st.Marches, March{ID: m.Id, From: m.From, To: m.To, ArriveAt: m.ArriveAt})
	}
	for _, q := range p.Queues {
		st.Queues = append(st.Queues, Queue{ID: q.Id, Kind: q.Kind, DoneAt: q.DoneAt, Canceled: q.Canceled})
	}
	for k, v := range p.Escrow {
		st.Escrow[k] = Assets{Gold: v.Gold, Wood: v.Wood, Food: v.Food}
	}
	for k, v := range p.SettledTrades {
		st.Settled[k] = v
	}
	return st
}
