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

// TCPRemote forwards commands over a dynamically sized pool of TCP links.
// A player is consistently mapped to one slot so its commands keep order at
// the gateway hop.
type TCPRemote struct {
	addr string

	mu    sync.RWMutex
	slots []*tcpSlot
}

type tcpSlot struct {
	mu   sync.Mutex
	conn *link.Client
}

// NewTCPRemote builds a pool with the given initial size.
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

// Size reports the current pool size.
func (r *TCPRemote) Size() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.slots)
}

// SetSize grows or shrinks the pool at runtime. Shrinking closes the removed
// slots; in-flight calls on them fail and are retried by the caller.
func (r *TCPRemote) SetSize(n int) {
	if n < 1 {
		n = 1
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for len(r.slots) < n {
		r.slots = append(r.slots, &tcpSlot{})
	}
	for len(r.slots) > n {
		last := r.slots[len(r.slots)-1]
		last.mu.Lock()
		if last.conn != nil {
			_ = last.conn.Close()
			last.conn = nil
		}
		last.mu.Unlock()
		r.slots = r.slots[:len(r.slots)-1]
	}
}

func (r *TCPRemote) pick(pid uint64) *tcpSlot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.slots) == 0 {
		return nil
	}
	// Knuth multiplicative hash; stable while the pool size is unchanged.
	idx := int((pid * 2654435761) % uint64(len(r.slots)))
	return r.slots[idx]
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

func (r *TCPRemote) call(ctx context.Context, pid uint64, payload []byte) ([]byte, error) {
	slot := r.pick(pid)
	if slot == nil {
		return nil, errors.New("tcp pool is empty")
	}
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
	data, err := r.call(ctx, req.PlayerID, payload)
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
	data, err := r.call(ctx, id, payload)
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
