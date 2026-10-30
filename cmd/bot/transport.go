package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"hapeserver/internal/link"
)

type transport struct {
	c     *link.Client
	token string
}

func dialTCP(addr string, pid uint64) (*transport, error) {
	c, err := link.Dial(addr, 2*time.Second)
	if err != nil {
		return nil, err
	}
	return &transport{c: c, token: fmt.Sprintf("dev:1:%d", pid)}, nil
}

func (t *transport) do(req apiRequest) (json.RawMessage, error) {
	body, _ := json.Marshal(struct {
		Token   string     `json:"token"`
		Request apiRequest `json:"request"`
	}{Token: t.token, Request: req})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	data, err := t.c.Call(ctx, body)
	if err != nil {
		return nil, err
	}
	var out struct {
		OK    bool            `json:"ok"`
		Data  json.RawMessage `json:"data"`
		Error string          `json:"error"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	if !out.OK {
		return nil, fmt.Errorf("%s", out.Error)
	}
	return out.Data, nil
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

	data, err := ta.do(apiRequest{PlayerID: a, Target: b, Action: "trade_create", Gold: 1, TargetGold: 0})
	if err != nil {
		return fmt.Errorf("create: %w", err)
	}
	var tr struct {
		ID string `json:"_id"`
	}
	if err := json.Unmarshal(data, &tr); err != nil || tr.ID == "" {
		return fmt.Errorf("create: bad trade id")
	}
	if _, err := tb.do(apiRequest{PlayerID: b, Action: "trade_accept", TradeID: tr.ID}); err != nil {
		return err
	}
	_, err = ta.do(apiRequest{PlayerID: a, Action: "trade_settle", TradeID: tr.ID})
	return err
}
