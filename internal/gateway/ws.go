package gateway

import (
	"context"
	"net/http"
	"time"

	"github.com/gorilla/websocket"

	"hapeserver/internal/world"
)

// Handler is the command surface behind the WebSocket gateway.
type Handler interface {
	Handle(ctx context.Context, req world.Request) (any, error)
}

// WS upgrades a connection and serves one JSON request/response per frame.
type WS struct {
	upgrader websocket.Upgrader
	handler  Handler
}

// NewWS builds the WebSocket handler.
func NewWS(h Handler) *WS {
	return &WS{
		upgrader: websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }},
		handler:  h,
	}
}

type wsResponse struct {
	Seq   uint64 `json:"seq"`
	OK    bool   `json:"ok"`
	Data  any    `json:"data,omitempty"`
	Error string `json:"error,omitempty"`
}

func (s *WS) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	sess, err := DevToken(r.Header.Get("Authorization"))
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()

	conn.SetReadLimit(1 << 20)
	_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	})

	for {
		var req world.Request
		if err := conn.ReadJSON(&req); err != nil {
			return
		}
		if req.PlayerID == 0 {
			req.PlayerID = sess.PlayerID
		}
		resp := wsResponse{Seq: req.Seq}
		if req.PlayerID != sess.PlayerID {
			resp.Error = "player mismatch"
		} else {
			data, err := s.handler.Handle(context.Background(), req)
			resp.OK = err == nil
			resp.Data = data
			if err != nil {
				resp.Error = err.Error()
			}
		}
		if err := conn.WriteJSON(resp); err != nil {
			return
		}
	}
}
