package main

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/protobuf/proto"

	"hapeserver/internal/link"
	"hapeserver/internal/protocol/playerv1"
	"hapeserver/internal/protocol/worldv1"
)

type transport struct {
	c       *link.Client
	token   string
	version atomic.Uint64
}

func dialTCP(addr string, pid uint64) (*transport, error) {
	c, err := link.Dial(addr, 2*time.Second)
	if err != nil {
		return nil, err
	}
	t := &transport{c: c, token: fmt.Sprintf("dev:1:%d", pid)}
	hello := &playerv1.Hello{Token: t.token}
	body, err := proto.Marshal(hello)
	if err != nil {
		_ = c.Close()
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	data, err := c.Call(ctx, body)
	if err != nil {
		_ = c.Close()
		return nil, err
	}
	var ack playerv1.HelloAck
	if err := proto.Unmarshal(data, &ack); err != nil || !ack.Ok {
		_ = c.Close()
		return nil, fmt.Errorf("hello failed: %v", ack.Error)
	}
	t.version.Store(ack.StateVersion)
	c.SetPushHandler(func(payload []byte) {
		var ev playerv1.Event
		if proto.Unmarshal(payload, &ev) == nil {
			t.version.Store(ev.StateVersion)
		}
	})
	return t, nil
}

func (t *transport) do(req apiRequest) (*worldv1.Response, error) {
	in := &playerv1.Request{
		Command: &worldv1.Command{
			PlayerId:    req.PlayerID,
			TargetId:    req.Target,
			Action:      req.Action,
			Gold:        req.Gold,
			TargetGold:  req.TargetGold,
			Power:       req.Power,
			TradeId:     req.TradeID,
			K:           int32(req.K),
			Season:      req.Season,
			DefPower:    req.DefPower,
			BattleId:    req.BattleID,
			FromVersion: req.FromVersion,
		},
	}
	body, err := proto.Marshal(in)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	data, err := t.c.Call(ctx, body)
	if err != nil {
		return nil, err
	}
	var out worldv1.Response
	if err := proto.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	if !out.Ok {
		return nil, fmt.Errorf("%s", out.Error)
	}
	return &out, nil
}

func (t *transport) close() { _ = t.c.Close() }

var peers sync.Map

func tradePeers(a, b uint64) error {
	va, ok := peers.Load(a)
	if !ok {
		return nil
	}
	vb, ok := peers.Load(b)
	if !ok {
		return nil
	}
	ta := va.(*transport)
	tb := vb.(*transport)

	resp, err := ta.do(apiRequest{PlayerID: a, Target: b, Action: "trade_create", Gold: 1, TargetGold: 0})
	if err != nil {
		return fmt.Errorf("create: %w", err)
	}
	id := resp.GetTrade().GetId()
	if id == "" {
		return fmt.Errorf("create: empty trade id")
	}
	if _, err := tb.do(apiRequest{PlayerID: b, Action: "trade_accept", TradeID: id}); err != nil {
		return err
	}
	_, err = ta.do(apiRequest{PlayerID: a, Action: "trade_settle", TradeID: id})
	return err
}
