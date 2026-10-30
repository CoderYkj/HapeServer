package gateway

import (
	"context"
	"encoding/json"
	"fmt"

	"google.golang.org/protobuf/proto"

	"hapeserver/internal/link"
	"hapeserver/internal/protocol/playerv1"
	"hapeserver/internal/world"
)

// Handler is the command surface behind the player TCP gateway.
type Handler interface {
	Handle(ctx context.Context, req world.Request) (any, error)
}

// NewPlayerHandler adapts a command handler to the protobuf v3 player
// protocol carried by the TCP framing layer.
func NewPlayerHandler(h Handler) link.Handler {
	return func(ctx context.Context, payload []byte) ([]byte, error) {
		var in playerv1.Request
		if err := proto.Unmarshal(payload, &in); err != nil {
			return nil, fmt.Errorf("bad request: %w", err)
		}
		sess, err := DevToken(in.Token)
		if err != nil {
			return marshalPlayerResponse(&playerv1.Response{Error: "unauthorized"})
		}
		req := world.Request{
			Seq:        in.Seq,
			PlayerID:   in.PlayerId,
			Target:     in.TargetId,
			Action:     in.Action,
			Gold:       in.Gold,
			TargetGold: in.TargetGold,
			Power:      in.Power,
			TradeID:    in.TradeId,
			K:          int(in.K),
			Season:     in.Season,
			DefPower:   in.DefPower,
			BattleID:   in.BattleId,
		}
		if req.PlayerID == 0 {
			req.PlayerID = sess.PlayerID
		}
		if req.PlayerID != sess.PlayerID {
			return marshalPlayerResponse(&playerv1.Response{Error: "player mismatch"})
		}
		data, herr := h.Handle(ctx, req)
		resp := &playerv1.Response{Ok: herr == nil}
		if herr != nil {
			resp.Error = herr.Error()
		} else if data != nil {
			b, err := json.Marshal(data)
			if err != nil {
				return nil, fmt.Errorf("marshal data: %w", err)
			}
			resp.Data = b
		}
		return marshalPlayerResponse(resp)
	}
}

func marshalPlayerResponse(resp *playerv1.Response) ([]byte, error) {
	b, err := proto.Marshal(resp)
	if err != nil {
		return nil, fmt.Errorf("marshal response: %w", err)
	}
	return b, nil
}
