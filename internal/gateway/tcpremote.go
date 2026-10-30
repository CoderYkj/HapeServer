package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"hapeserver/internal/link"
	"hapeserver/internal/world"
)

// TCPRemote forwards commands over a multiplexed TCP link to one world.
type TCPRemote struct {
	addr string
	mu   sync.Mutex
	conn *link.Client
}

// NewTCPRemote builds a handler for one world TCP endpoint.
func NewTCPRemote(addr string) *TCPRemote {
	return &TCPRemote{addr: addr}
}

func (r *TCPRemote) client() (*link.Client, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.conn != nil {
		return r.conn, nil
	}
	c, err := link.Dial(r.addr, 2*time.Second)
	if err != nil {
		return nil, err
	}
	r.conn = c
	return c, nil
}

func (r *TCPRemote) drop(c *link.Client) {
	r.mu.Lock()
	if r.conn == c {
		r.conn = nil
	}
	r.mu.Unlock()
	_ = c.Close()
}

// Handle implements gateway.Handler.
func (r *TCPRemote) Handle(ctx context.Context, req world.Request) (any, error) {
	c, err := r.client()
	if err != nil {
		return nil, err
	}
	payload, _ := json.Marshal(req)
	data, err := c.Call(ctx, payload)
	if err != nil {
		r.drop(c)
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
		return nil, errors.New(out.Error)
	}
	return out.Data, nil
}

// State implements StateProvider for resume/push.
func (r *TCPRemote) State(id uint64) (world.PlayerState, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c, err := r.client()
	if err != nil {
		return world.PlayerState{}, false
	}
	payload, _ := json.Marshal(world.Request{PlayerID: id, Action: "state"})
	data, err := c.Call(ctx, payload)
	if err != nil {
		r.drop(c)
		return world.PlayerState{}, false
	}
	var out struct {
		OK   bool              `json:"ok"`
		Data world.PlayerState `json:"data"`
	}
	if err := json.Unmarshal(data, &out); err != nil || !out.OK {
		return world.PlayerState{}, false
	}
	return out.Data, true
}
