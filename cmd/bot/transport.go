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
	conn *websocket.Conn
	mu   sync.Mutex
	seq  uint64
}

func dialWS(base string, pid uint64) (*wsClient, error) {
	u := strings.Replace(base, "http://", "ws://", 1)
	u = strings.TrimRight(u, "/") + "/ws"
	hdr := http.Header{"Authorization": []string{fmt.Sprintf("dev:1:%d", pid)}}
	conn, _, err := websocket.DefaultDialer.Dial(u, hdr)
	if err != nil {
		return nil, err
	}
	return &wsClient{conn: conn}, nil
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
	var out struct {
		Seq   uint64          `json:"seq"`
		OK    bool            `json:"ok"`
		Data  json.RawMessage `json:"data"`
		Error string          `json:"error"`
	}
	if err := c.conn.ReadJSON(&out); err != nil {
		return nil, err
	}
	if !out.OK {
		return nil, fmt.Errorf("%s", out.Error)
	}
	return out.Data, nil
}

func (c *wsClient) close() { _ = c.conn.Close() }
