package gateway

import (
	"context"
	"encoding/json"
	"fmt"

	"hapeserver/internal/link"
	"hapeserver/internal/world"
)

// Handler is the command surface behind the player TCP gateway.
type Handler interface {
	Handle(ctx context.Context, req world.Request) (any, error)
}

// tcpEnvelope is one player request. The dev token is sent per request; JWT
// session binding replaces it in P1.
type tcpEnvelope struct {
	Token   string        `json:"token"`
	Request world.Request `json:"request"`
}

type tcpResponse struct {
	OK    bool   `json:"ok"`
	Data  any    `json:"data,omitempty"`
	Error string `json:"error,omitempty"`
}

// NewPlayerHandler adapts a command handler to the raw TCP player protocol.
func NewPlayerHandler(h Handler) link.Handler {
	return func(ctx context.Context, payload []byte) ([]byte, error) {
		var in tcpEnvelope
		if err := json.Unmarshal(payload, &in); err != nil {
			return nil, fmt.Errorf("bad request: %w", err)
		}
		sess, err := DevToken(in.Token)
		if err != nil {
			return json.Marshal(tcpResponse{Error: "unauthorized"})
		}
		req := in.Request
		if req.PlayerID == 0 {
			req.PlayerID = sess.PlayerID
		}
		if req.PlayerID != sess.PlayerID {
			return json.Marshal(tcpResponse{Error: "player mismatch"})
		}
		data, herr := h.Handle(ctx, req)
		resp := tcpResponse{OK: herr == nil, Data: data}
		if herr != nil {
			resp.Error = herr.Error()
		}
		return json.Marshal(resp)
	}
}
