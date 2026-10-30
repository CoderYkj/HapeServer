package gateway

import (
	"context"
	"errors"

	"google.golang.org/protobuf/proto"

	"hapeserver/internal/link"
	"hapeserver/internal/protocol/playerv1"
	"hapeserver/internal/protocol/worldv1"
	"hapeserver/internal/world"
)

// NewPlayerConnHandler serves a player TCP connection: hello/resume, then
// request/response with optional state pushes.
func NewPlayerConnHandler(h Handler) link.ConnHandler {
	return func(ctx context.Context, c *link.Conn) {
		id, payload, err := c.Read()
		if err != nil {
			return
		}
		var hello playerv1.Hello
		if err := proto.Unmarshal(payload, &hello); err != nil {
			_ = replyHello(c, id, &playerv1.HelloAck{Error: "bad hello"})
			return
		}
		sess, err := DevToken(hello.Token)
		if err != nil {
			_ = replyHello(c, id, &playerv1.HelloAck{Error: "unauthorized"})
			return
		}
		st, ok := h.State(ctx, sess.PlayerID)
		version := uint64(0)
		if ok && st != nil {
			version = st.Version
		}
		ack := &playerv1.HelloAck{Ok: true, StateVersion: version}
		if ok && hello.LastStateVersion != version {
			if d, derr := loadDelta(ctx, h, sess.PlayerID, hello.LastStateVersion); derr == nil {
				ack.Delta = d
			} else {
				ack.State = st
			}
		}
		if err := replyHello(c, id, ack); err != nil {
			return
		}
		lastVersion := version

		for {
			id, payload, err := c.Read()
			if err != nil {
				return
			}
			var in playerv1.Request
			if err := proto.Unmarshal(payload, &in); err != nil {
				_ = replyWorld(c, id, &worldv1.Response{Error: "bad request"})
				continue
			}
			if in.Token != "" {
				s, err := DevToken(in.Token)
				if err != nil || s.PlayerID != sess.PlayerID {
					_ = replyWorld(c, id, &worldv1.Response{Error: "unauthorized"})
					continue
				}
			}
			cmd := in.GetCommand()
			if cmd == nil {
				_ = replyWorld(c, id, &worldv1.Response{Error: "missing command"})
				continue
			}
			if cmd.PlayerId == 0 {
				cmd.PlayerId = sess.PlayerID
			}
			if cmd.PlayerId != sess.PlayerID {
				_ = replyWorld(c, id, &worldv1.Response{Error: "player mismatch"})
				continue
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
			resp, herr := h.Handle(ctx, req)
			if herr != nil {
				resp = &worldv1.Response{Error: herr.Error()}
			}
			if resp == nil {
				resp = &worldv1.Response{}
			}
			if err := replyWorld(c, id, resp); err != nil {
				return
			}
			if hello.Subscribe && resp.Ok && isMutation(req.Action) {
				if st, ok := h.State(ctx, sess.PlayerID); ok && st != nil {
					ev := &playerv1.Event{StateVersion: st.Version}
					if d, derr := loadDelta(ctx, h, sess.PlayerID, lastVersion); derr == nil {
						ev.Delta = d
					} else {
						ev.State = st
					}
					b, err := proto.Marshal(ev)
					if err != nil || c.Push(b) != nil {
						return
					}
					lastVersion = st.Version
				}
			}
		}
	}
}

func replyHello(c *link.Conn, id uint64, ack *playerv1.HelloAck) error {
	b, err := proto.Marshal(ack)
	if err != nil {
		return err
	}
	return c.Reply(id, b)
}

func replyWorld(c *link.Conn, id uint64, resp *worldv1.Response) error {
	b, err := proto.Marshal(resp)
	if err != nil {
		return err
	}
	return c.Reply(id, b)
}

func isMutation(action string) bool {
	switch action {
	case "state", "rank", "top", "trade_get", "battle_get", "battle_history":
		return false
	default:
		return true
	}
}

func loadDelta(ctx context.Context, h Handler, pid, from uint64) (*worldv1.StateDelta, error) {
	resp, err := h.Handle(ctx, world.Request{PlayerID: pid, Action: "state_delta", FromVersion: from})
	if err != nil {
		return nil, err
	}
	if resp == nil || !resp.Ok {
		return nil, errors.New("state delta unavailable")
	}
	d := resp.GetDelta()
	if d == nil {
		return nil, errors.New("state delta unavailable")
	}
	return d, nil
}
