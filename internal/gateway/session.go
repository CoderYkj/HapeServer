// Package gateway holds connection-scoped state. The P0 token is a dev token;
// JWT + refresh lands in P1.
package gateway

import (
	"errors"
	"strings"
)

// Session is one authenticated WebSocket connection.
type Session struct {
	PlayerID uint64
	WorldID  uint32
	Region   string
	Seq      uint64
	Version  uint64
}

// ErrUnauthorized is returned for malformed or missing tokens.
var ErrUnauthorized = errors.New("unauthorized")

// DevToken parses "dev:<world>:<player>" for local tests.
func DevToken(token string) (Session, error) {
	parts := strings.Split(token, ":")
	if len(parts) != 3 || parts[0] != "dev" {
		return Session{}, ErrUnauthorized
	}
	var world uint32
	var player uint64
	for _, c := range parts[1] {
		if c < '0' || c > '9' {
			return Session{}, ErrUnauthorized
		}
		world = world*10 + uint32(c-'0')
	}
	for _, c := range parts[2] {
		if c < '0' || c > '9' {
			return Session{}, ErrUnauthorized
		}
		player = player*10 + uint64(c-'0')
	}
	return Session{PlayerID: player, WorldID: world, Region: "dev"}, nil
}
