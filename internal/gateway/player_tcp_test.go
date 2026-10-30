package gateway

import (
	"context"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"hapeserver/internal/link"
	"hapeserver/internal/protocol/playerv1"
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

func callTCP(t *testing.T, srv *link.Server, in *playerv1.Request) *playerv1.Response {
	t.Helper()
	c, err := link.Dial(srv.Addr(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	body, err := proto.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	data, err := c.Call(context.Background(), body)
	if err != nil {
		t.Fatal(err)
	}
	var out playerv1.Response
	if err := proto.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return &out
}

func TestPlayerTCPCommand(t *testing.T) {
	srv := startPlayerTCP(t)
	resp := callTCP(t, srv, &playerv1.Request{Token: "dev:1:7", PlayerId: 7, Action: "rank"})
	if !resp.Ok {
		t.Fatalf("resp = %+v", resp)
	}
}

func TestPlayerTCPAuth(t *testing.T) {
	srv := startPlayerTCP(t)
	resp := callTCP(t, srv, &playerv1.Request{Token: "bad", PlayerId: 1, Action: "rank"})
	if resp.Ok || resp.Error != "unauthorized" {
		t.Fatalf("resp = %+v", resp)
	}
}

func TestPlayerTCPMismatch(t *testing.T) {
	srv := startPlayerTCP(t)
	resp := callTCP(t, srv, &playerv1.Request{Token: "dev:1:7", PlayerId: 8, Action: "rank"})
	if resp.Ok || resp.Error != "player mismatch" {
		t.Fatalf("resp = %+v", resp)
	}
}
