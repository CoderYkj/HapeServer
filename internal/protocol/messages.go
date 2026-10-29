// Package protocol defines the wire messages between gateway, world and bot.
// P0 uses JSON; the protobuf schema replaces it in P1.
package protocol

// Request is a client command.
type Request struct {
	Seq    uint64 `json:"seq"`
	OpID   string `json:"opId"`
	Action string `json:"action"`
	Player uint64 `json:"playerId"`
	Target uint64 `json:"targetId,omitempty"`
	Amount int64  `json:"amount,omitempty"`
}

// Response is the server reply to one Request.
type Response struct {
	Seq     uint64 `json:"seq"`
	OK      bool   `json:"ok"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

// Push is a server-initiated state update.
type Push struct {
	StateVersion uint64 `json:"stateVersion"`
	Kind         string `json:"kind"`
	Payload      []byte `json:"payload,omitempty"`
}
