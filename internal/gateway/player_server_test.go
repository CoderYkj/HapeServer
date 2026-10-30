package gateway

import (
	"context"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"hapeserver/internal/link"
	"hapeserver/internal/protocol/playerv1"
	"hapeserver/internal/protocol/worldv1"
)

func startConnServer(t *testing.T) *link.PlayerServer {
	t.Helper()
	srv, err := link.ListenPlayer("127.0.0.1:0", NewPlayerConnHandler(echoHandler{}))
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(context.Background()) }()
	t.Cleanup(func() { _ = srv.Close() })
	return srv
}

func hello(t *testing.T, c *link.Client, token string, v uint64, subscribe bool) *playerv1.HelloAck {
	t.Helper()
	body, _ := proto.Marshal(&playerv1.Hello{Token: token, LastStateVersion: v, Subscribe: subscribe})
	data, err := c.Call(context.Background(), body)
	if err != nil {
		t.Fatal(err)
	}
	var ack playerv1.HelloAck
	if err := proto.Unmarshal(data, &ack); err != nil {
		t.Fatal(err)
	}
	return &ack
}

func TestPlayerConnPushAndResume(t *testing.T) {
	srv := startConnServer(t)
	c, err := link.Dial(srv.Addr(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	ack := hello(t, c, "dev:1:7", 0, true)
	if !ack.Ok || ack.StateVersion != 1 || ack.State == nil {
		t.Fatalf("ack = %+v", ack)
	}

	push := make(chan *playerv1.Event, 1)
	c.SetPushHandler(func(b []byte) {
		var ev playerv1.Event
		if proto.Unmarshal(b, &ev) == nil {
			select {
			case push <- &ev:
			default:
			}
		}
	})

	req, _ := proto.Marshal(&playerv1.Request{Command: &worldv1.Command{PlayerId: 7, Action: "setpower", Power: 10}})
	data, err := c.Call(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	var resp worldv1.Response
	if err := proto.Unmarshal(data, &resp); err != nil || !resp.Ok {
		t.Fatalf("resp ok=%v error=%q err=%v", resp.Ok, resp.Error, err)
	}
	select {
	case ev := <-push:
		if ev.StateVersion != 1 || (ev.State == nil && ev.Delta == nil) {
			t.Fatalf("event = %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("no push event")
	}

	// Resume from the same version gets no full state.
	c2, _ := link.Dial(srv.Addr(), time.Second)
	defer func() { _ = c2.Close() }()
	ack2 := hello(t, c2, "dev:1:7", 1, false)
	if ack2.StateVersion != 1 || ack2.State != nil {
		t.Fatalf("resume ack = %+v", ack2)
	}
}
