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

// StateProvider exposes a player's current state for pushes and resumes.
type StateProvider interface {
	State(id uint64) (world.PlayerState, bool)
}

// WS upgrades a connection and serves JSON commands plus optional pushes.
type WS struct {
	upgrader websocket.Upgrader
	handler  Handler
	provider StateProvider
}

// NewWS builds the WebSocket handler.
func NewWS(h Handler) *WS {
	w := &WS{
		upgrader: websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }},
		handler:  h,
	}
	if p, ok := h.(StateProvider); ok {
		w.provider = p
	}
	return w
}

type wsRequest struct {
	Type string `json:"type"`
	world.Request
}

type wsResponse struct {
	Type         string `json:"type"`
	Seq          uint64 `json:"seq,omitempty"`
	OK           bool   `json:"ok"`
	StateVersion uint64 `json:"stateVersion,omitempty"`
	Data         any    `json:"data,omitempty"`
	Error        string `json:"error,omitempty"`
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

	subscribed := false
	for {
		var req wsRequest
		if err := conn.ReadJSON(&req); err != nil {
			return
		}
		_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		pid := req.PlayerID
		if pid == 0 {
			pid = sess.PlayerID
		}
		if pid != sess.PlayerID {
			if err := conn.WriteJSON(wsResponse{Type: "error", Seq: req.Seq, Error: "player mismatch"}); err != nil {
				return
			}
			continue
		}
		req.PlayerID = pid

		switch req.Type {
		case "subscribe":
			subscribed = true
			if err := conn.WriteJSON(wsResponse{Type: "subscribed", OK: true}); err != nil {
				return
			}
		case "resume":
			if s.provider == nil {
				_ = conn.WriteJSON(wsResponse{Type: "error", Error: "resume unsupported"})
				continue
			}
			st, ok := s.provider.State(pid)
			if !ok {
				// A fresh player resumes at version 0; the first command creates it.
				if err := conn.WriteJSON(wsResponse{Type: "resume", OK: true, StateVersion: 0}); err != nil {
					return
				}
				continue
			}
			if err := conn.WriteJSON(wsResponse{Type: "resume", OK: true, StateVersion: st.Version, Data: st}); err != nil {
				return
			}
		default:
			data, herr := s.handler.Handle(context.Background(), req.Request)
			resp := wsResponse{Type: "response", Seq: req.Seq, OK: herr == nil, Data: data}
			if herr != nil {
				resp.Error = herr.Error()
			}
			if err := conn.WriteJSON(resp); err != nil {
				return
			}
			if subscribed && s.provider != nil {
				if st, ok := s.provider.State(pid); ok {
					if err := conn.WriteJSON(wsResponse{Type: "push", StateVersion: st.Version, Data: st}); err != nil {
						return
					}
				}
			}
		}
	}
}
