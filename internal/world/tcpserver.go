package world

import (
	"context"
	"encoding/json"

	"hapeserver/internal/link"
)

// NewTCPHandler exposes Service.Handle over the framed TCP link.
func NewTCPHandler(s *Service) link.Handler {
	return func(ctx context.Context, payload []byte) ([]byte, error) {
		var req Request
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, err
		}
		data, err := s.Handle(ctx, req)
		resp := struct {
			OK    bool   `json:"ok"`
			Data  any    `json:"data,omitempty"`
			Error string `json:"error,omitempty"`
		}{OK: err == nil, Data: data}
		if err != nil {
			resp.Error = err.Error()
		}
		return json.Marshal(resp)
	}
}
