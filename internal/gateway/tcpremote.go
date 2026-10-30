package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"hapeserver/internal/link"
	"hapeserver/internal/world"
)

// TCPRemote forwards commands over a pool of multiplexed TCP links.
type TCPRemote struct {
	addr  string
	slots []*tcpSlot
	next  atomic.Uint64
}

type tcpSlot struct {
	mu   sync.Mutex
	conn *link.Client
}

// NewTCPRemote builds a pool of poolSize connections to one world.
func NewTCPRemote(addr string, poolSize int) *TCPRemote {
	if poolSize <= 0 {
		poolSize = 4
	}
	r := &TCPRemote{addr: addr, slots: make([]*tcpSlot, poolSize)}
	for i := range r.slots {
		r.slots[i] = &tcpSlot{}
	}
	return r
}

func (r *TCPRemote) pick() *tcpSlot {
	i := r.next.Add(1)
	return r.slots[int(i)%len(r.slots)]
}

func (r *TCPRemote) client(s *tcpSlot) (*link.Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn != nil {
		return s.conn, nil
	}
	c, err := link.Dial(r.addr, 2*time.Second)
	if err != nil {
		return nil, err
	}
	s.conn = c
	return c, nil
}

func (r *TCPRemote) drop(s *tcpSlot, c *link.Client) {
	s.mu.Lock()
	if s.conn == c {
		s.conn = nil
	}
	s.mu.Unlock()
	_ = c.Close()
}

func (r *TCPRemote) call(ctx context.Context, payload []byte) ([]byte, error) {
	slot := r.pick()
	c, err := r.client(slot)
	if err != nil {
		return nil, err
	}
	data, err := c.Call(ctx, payload)
	if err != nil {
		r.drop(slot, c)
		return nil, err
	}
	return data, nil
}

// Handle implements gateway.Handler.
func (r *TCPRemote) Handle(ctx context.Context, req world.Request) (any, error) {
	payload, _ := json.Marshal(req)
	data, err := r.call(ctx, payload)
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
		return nil, errors.New(out.Error)
	}
	return out.Data, nil
}

// State implements StateProvider for resume/push.
func (r *TCPRemote) State(id uint64) (world.PlayerState, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	payload, _ := json.Marshal(world.Request{PlayerID: id, Action: "state"})
	data, err := r.call(ctx, payload)
	if err != nil {
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
