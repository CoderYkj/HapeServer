package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// transport sends one command over HTTP or a persistent WebSocket.
type transport struct {
	hc   *http.Client
	base string
	ws   *wsClient
}

func (t *transport) do(req apiRequest) (json.RawMessage, error) {
	if t.ws != nil {
		return t.ws.do(req)
	}
	return t.httpDo(req)
}

func (t *transport) httpDo(req apiRequest) (json.RawMessage, error) {
	body, _ := json.Marshal(req)
	resp, err := t.hc.Post(t.base+"/api/command", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	var out apiResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if !out.OK {
		return nil, fmt.Errorf("%s", out.Error)
	}
	return out.Data, nil
}

func (t *transport) trade(a, b uint64) error {
	data, err := t.httpDo(apiRequest{PlayerID: a, Target: b, Action: "trade_create", Gold: 1, TargetGold: 0})
	if err != nil {
		return fmt.Errorf("create: %w", err)
	}
	var tr struct {
		ID string `json:"_id"`
	}
	if err := json.Unmarshal(data, &tr); err != nil || tr.ID == "" {
		return fmt.Errorf("create: bad trade id")
	}
	if _, err := t.httpDo(apiRequest{PlayerID: b, Action: "trade_accept", TradeID: tr.ID}); err != nil {
		return err
	}
	_, err = t.httpDo(apiRequest{PlayerID: a, Action: "trade_settle", TradeID: tr.ID})
	return err
}

type wsClient struct {
	conn    *websocket.Conn
	mu      sync.Mutex
	seq     uint64
	version uint64
}

func dialWS(base string, pid uint64) (*wsClient, error) {
	u := strings.Replace(base, "http://", "ws://", 1)
	u = strings.TrimRight(u, "/") + "/ws"
	hdr := http.Header{"Authorization": []string{fmt.Sprintf("dev:1:%d", pid)}}
	conn, _, err := websocket.DefaultDialer.Dial(u, hdr)
	if err != nil {
		return nil, err
	}
	c := &wsClient{conn: conn}
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if err := conn.WriteJSON(map[string]any{"type": "resume", "playerId": pid}); err != nil {
		_ = conn.Close()
		return nil, err
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var resumed struct {
		Type         string `json:"type"`
		OK           bool   `json:"ok"`
		StateVersion uint64 `json:"stateVersion"`
		Error        string `json:"error"`
	}
	if err := conn.ReadJSON(&resumed); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if resumed.Type != "resume" || !resumed.OK {
		_ = conn.Close()
		return nil, fmt.Errorf("resume failed: %s", resumed.Error)
	}
	c.version = resumed.StateVersion
	return c, nil
}

func (c *wsClient) do(req apiRequest) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seq++
	req.Seq = c.seq
	_ = c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if err := c.conn.WriteJSON(req); err != nil {
		return nil, err
	}
	_ = c.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		var out struct {
			Type         string          `json:"type"`
			Seq          uint64          `json:"seq"`
			OK           bool            `json:"ok"`
			StateVersion uint64          `json:"stateVersion"`
			Data         json.RawMessage `json:"data"`
			Error        string          `json:"error"`
		}
		if err := c.conn.ReadJSON(&out); err != nil {
			return nil, err
		}
		if out.Type == "push" {
			c.version = out.StateVersion
			continue
		}
		if out.Type != "" && out.Type != "response" {
			continue
		}
		if !out.OK {
			return nil, fmt.Errorf("%s", out.Error)
		}
		return out.Data, nil
	}
}

func (c *wsClient) close() { _ = c.conn.Close() }

var peers sync.Map

// tradePeers runs a trade across each player's own transport, so both WS
// sessions keep their own identity.
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
