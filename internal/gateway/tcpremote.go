package gateway

import (
	"context"
	"errors"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	"hapeserver/internal/link"
	"hapeserver/internal/protocol/worldv1"
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

// Handle implements gateway.Handler over the typed world link.
func (r *TCPRemote) Handle(ctx context.Context, req world.Request) (*worldv1.Response, error) {
	cmd := &worldv1.Command{
		Seq:        req.Seq,
		PlayerId:   req.PlayerID,
		Action:     req.Action,
		TargetId:   req.Target,
		Gold:       req.Gold,
		TargetGold: req.TargetGold,
		Power:      req.Power,
		TradeId:    req.TradeID,
		K:          int32(req.K),
		Season:     req.Season,
		DefPower:   req.DefPower,
		BattleId:   req.BattleID,
	}
	payload, err := proto.Marshal(cmd)
	if err != nil {
		return nil, err
	}
	data, err := r.call(ctx, req.PlayerID, payload)
	if err != nil {
		return nil, err
	}
	var resp worldv1.Response
	if err := proto.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// State loads the player's current state for resume and push.
func (r *TCPRemote) State(ctx context.Context, playerID uint64) (*worldv1.PlayerState, bool) {
	cmd := &worldv1.Command{PlayerId: playerID, Action: "state"}
	payload, err := proto.Marshal(cmd)
	if err != nil {
		return nil, false
	}
	data, err := r.call(ctx, playerID, payload)
	if err != nil {
		return nil, false
	}
	var resp worldv1.Response
	if err := proto.Unmarshal(data, &resp); err != nil || !resp.Ok {
		return nil, false
	}
	return resp.GetState(), true
}
