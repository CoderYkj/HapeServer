package world

import (
	"context"

	"google.golang.org/protobuf/proto"

	"hapeserver/internal/link"
	"hapeserver/internal/protocol/worldv1"
)

// NewTCPHandler exposes Service.Handle over the typed protobuf world link.
func NewTCPHandler(s *Service) link.Handler {
	return func(ctx context.Context, payload []byte) ([]byte, error) {
		var cmd worldv1.Command
		if err := proto.Unmarshal(payload, &cmd); err != nil {
			return nil, err
		}
		req := Request{
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
		resp, _ := s.Handle(ctx, req)
		return proto.Marshal(resp)
	}
}
