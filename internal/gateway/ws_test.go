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
