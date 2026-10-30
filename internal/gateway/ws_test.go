package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"

	"hapeserver/internal/world"
)

type fakeHandler struct{}

func (fakeHandler) Handle(_ context.Context, req world.Request) (any, error) {
	return map[string]any{"action": req.Action}, nil
}

func TestWSCommand(t *testing.T) {
	srv := httptest.NewServer(NewWS(fakeHandler{}))
	defer srv.Close()
	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	hdr := http.Header{"Authorization": []string{"dev:1:7"}}
	conn, _, err := websocket.DefaultDialer.Dial(url, hdr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()

	if err := conn.WriteJSON(world.Request{Seq: 1, PlayerID: 7, Action: "rank"}); err != nil {
		t.Fatal(err)
	}
	var resp wsResponse
	if err := conn.ReadJSON(&resp); err != nil {
		t.Fatal(err)
	}
	if !resp.OK || resp.Seq != 1 {
		t.Fatalf("resp = %+v", resp)
	}
}

func TestWSRejectsOtherPlayer(t *testing.T) {
	srv := httptest.NewServer(NewWS(fakeHandler{}))
	defer srv.Close()
	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	hdr := http.Header{"Authorization": []string{"dev:1:7"}}
	conn, _, err := websocket.DefaultDialer.Dial(url, hdr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.WriteJSON(world.Request{Seq: 2, PlayerID: 8, Action: "rank"})
	var resp wsResponse
	if err := conn.ReadJSON(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.OK || resp.Error == "" {
		t.Fatalf("resp = %+v", resp)
	}
}

type stateHandler struct {
	fakeHandler
	state world.PlayerState
}

func (s *stateHandler) State(id uint64) (world.PlayerState, bool) {
	if id != s.state.PlayerID {
		return world.PlayerState{}, false
	}
	return s.state, true
}

func TestWSPushAndResume(t *testing.T) {
	h := &stateHandler{state: world.PlayerState{PlayerID: 7, Version: 3}}
	srv := httptest.NewServer(NewWS(h))
	defer srv.Close()
	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	hdr := http.Header{"Authorization": []string{"dev:1:7"}}

	conn, _, err := websocket.DefaultDialer.Dial(url, hdr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()

	if err := conn.WriteJSON(map[string]any{"type": "subscribe"}); err != nil {
		t.Fatal(err)
	}
	var sub wsResponse
	if err := conn.ReadJSON(&sub); err != nil {
		t.Fatal(err)
	}
	if sub.Type != "subscribed" {
		t.Fatalf("sub = %+v", sub)
	}

	if err := conn.WriteJSON(world.Request{Seq: 1, PlayerID: 7, Action: "rank"}); err != nil {
		t.Fatal(err)
	}
	var resp wsResponse
	if err := conn.ReadJSON(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Type != "response" || !resp.OK {
		t.Fatalf("resp = %+v", resp)
	}
	var push wsResponse
	if err := conn.ReadJSON(&push); err != nil {
		t.Fatal(err)
	}
	if push.Type != "push" || push.StateVersion != 3 {
		t.Fatalf("push = %+v", push)
	}

	conn2, _, err := websocket.DefaultDialer.Dial(url, hdr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn2.Close() }()
	if err := conn2.WriteJSON(map[string]any{"type": "resume", "playerId": 7}); err != nil {
		t.Fatal(err)
	}
	var resumed wsResponse
	if err := conn2.ReadJSON(&resumed); err != nil {
		t.Fatal(err)
	}
	if resumed.Type != "resume" || !resumed.OK || resumed.StateVersion != 3 {
		t.Fatalf("resume = %+v", resumed)
	}
}
