package gateway

import (
	"context"

	"google.golang.org/protobuf/proto"

	"hapeserver/internal/link"
	"hapeserver/internal/protocol/playerv1"
	"hapeserver/internal/protocol/worldv1"
	"hapeserver/internal/world"
)

// Handler is the command surface behind the player TCP gateway.
type Handler interface {
	Handle(ctx context.Context, req world.Request) (*worldv1.Response, error)
}

// NewPlayerHandler adapts the typed world handler to the protobuf v3 player
// protocol carried by the TCP framing layer.
func NewPlayerHandler(h Handler) link.Handler {
	return func(ctx context.Context, payload []byte) ([]byte, error) {
		var in playerv1.Request
		if err := proto.Unmarshal(payload, &in); err != nil {
			return nil, err
		}
		sess, err := DevToken(in.Token)
		if err != nil {
			return marshalResponse(&worldv1.Response{Error: "unauthorized"})
		}
		cmd := in.GetCommand()
		if cmd == nil {
			return marshalResponse(&worldv1.Response{Error: "missing command"})
		}
		req := world.Request{
			Seq:        cmd.Seq,
			PlayerID:   cmd.PlayerId,
			Target:     cmd.TargetId,
			Action:     cmd.Action,
			Gold:       cmd.Gold,
			TargetGold: cmd.TargetGold,
			Power:      cmd.Power,
			TradeID:    cmd.TradeId,
			K:          int(cmd.K),
			Season:     cmd.Season,
			DefPower:   cmd.DefPower,
			BattleID:   cmd.BattleId,
		}
		if req.PlayerID == 0 {
			req.PlayerID = sess.PlayerID
		}
		if req.PlayerID != sess.PlayerID {
			return marshalResponse(&worldv1.Response{Error: "player mismatch"})
		}
		resp, herr := h.Handle(ctx, req)
		if herr != nil {
			resp = &worldv1.Response{Error: herr.Error()}
		}
		if resp == nil {
			resp = &worldv1.Response{}
		}
		return marshalResponse(resp)
	}
}

func marshalResponse(resp *worldv1.Response) ([]byte, error) {
	return proto.Marshal(resp)
}
