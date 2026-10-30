package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"hapeserver/internal/world"
)

// Remote forwards commands to the world process over HTTP. It keeps the
// gateway stateless: WS sessions live here, authoritative state stays in world.
type Remote struct {
	base string
	hc   *http.Client
}

// NewRemote builds a handler for one world endpoint.
func NewRemote(base string) *Remote {
	return &Remote{
		base: base,
		hc:   &http.Client{Timeout: 5 * time.Second},
	}
}

type remoteResponse struct {
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data"`
	Error string          `json:"error"`
}

func (r *Remote) Handle(_ context.Context, req world.Request) (any, error) {
	body, _ := json.Marshal(req)
	resp, err := r.hc.Post(r.base+"/api/command", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	var out remoteResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if !out.OK {
		return nil, fmt.Errorf("%s", out.Error)
	}
	return out.Data, nil
}

// State fetches a player snapshot for resume/push.
func (r *Remote) State(id uint64) (world.PlayerState, bool) {
	body, _ := json.Marshal(world.Request{PlayerID: id, Action: "state"})
	resp, err := r.hc.Post(r.base+"/api/command", "application/json", bytes.NewReader(body))
	if err != nil {
		return world.PlayerState{}, false
	}
	defer func() { _ = resp.Body.Close() }()
	var out remoteResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || !out.OK {
		return world.PlayerState{}, false
	}
	var st world.PlayerState
	if err := json.Unmarshal(out.Data, &st); err != nil {
		return world.PlayerState{}, false
	}
	return st, true
}
