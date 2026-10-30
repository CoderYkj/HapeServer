package gateway

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"hapeserver/internal/link"
	"hapeserver/internal/world"
)

type echoHandler struct{}

func (echoHandler) Handle(_ context.Context, req world.Request) (any, error) {
	return map[string]any{"action": req.Action, "player": req.PlayerID}, nil
}

func startPlayerTCP(t *testing.T) *link.Server {
	t.Helper()
	srv, err := link.Listen("127.0.0.1:0", NewPlayerHandler(echoHandler{}))
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(context.Background()) }()
	t.Cleanup(func() { _ = srv.Close() })
	return srv
}

func TestPlayerTCPCommand(t *testing.T) {
	srv := startPlayerTCP(t)
	c, err := link.Dial(srv.Addr(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	body, _ := json.Marshal(tcpEnvelope{Token: "dev:1:7", Request: world.Request{PlayerID: 7, Action: "rank"}})
	data, err := c.Call(context.Background(), body)
	if err != nil {
		t.Fatal(err)
	}
	var resp tcpResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatalf("resp = %+v", resp)
	}
}

func TestPlayerTCPAuth(t *testing.T) {
	srv := startPlayerTCP(t)
	c, _ := link.Dial(srv.Addr(), time.Second)
	defer func() { _ = c.Close() }()

	body, _ := json.Marshal(tcpEnvelope{Token: "bad", Request: world.Request{PlayerID: 1, Action: "rank"}})
	data, err := c.Call(context.Background(), body)
	if err != nil {
		t.Fatal(err)
	}
	var resp tcpResponse
	_ = json.Unmarshal(data, &resp)
	if resp.OK || resp.Error != "unauthorized" {
		t.Fatalf("resp = %+v", resp)
	}
}

func TestPlayerTCPMismatch(t *testing.T) {
	srv := startPlayerTCP(t)
	c, _ := link.Dial(srv.Addr(), time.Second)
	defer func() { _ = c.Close() }()

	body, _ := json.Marshal(tcpEnvelope{Token: "dev:1:7", Request: world.Request{PlayerID: 8, Action: "rank"}})
	data, err := c.Call(context.Background(), body)
	if err != nil {
		t.Fatal(err)
	}
	var resp tcpResponse
	_ = json.Unmarshal(data, &resp)
	if resp.OK || resp.Error != "player mismatch" {
		t.Fatalf("resp = %+v", resp)
	}
}
