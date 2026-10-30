package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"hapeserver/internal/world"
)

func TestRemoteHandleAndState(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req world.Request
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		if req.Action == "state" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":   true,
				"data": world.PlayerState{PlayerID: req.PlayerID, Version: 4},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": map[string]any{"action": req.Action}})
	}))
	defer srv.Close()

	r := NewRemote(srv.URL)
	data, err := r.Handle(nil, world.Request{PlayerID: 7, Action: "rank"})
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(data.(json.RawMessage), &out); err != nil {
		t.Fatal(err)
	}
	if out["action"] != "rank" {
		t.Fatalf("data = %v", out)
	}
	st, ok := r.State(7)
	if !ok || st.PlayerID != 7 || st.Version != 4 {
		t.Fatalf("state = %+v ok=%v", st, ok)
	}
}
