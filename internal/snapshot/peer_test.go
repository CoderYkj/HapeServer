package snapshot

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPeerReplication(t *testing.T) {
	peer, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(ServeReplica(peer, "secret"))
	defer srv.Close()

	rs, err := NewReplicaSetWithPeers([]string{t.TempDir()}, 2, []string{srv.URL}, "secret")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rs.Save(1, 1, 5, []byte("state-5")); err != nil {
		t.Fatal(err)
	}
	if h, payload, _, err := peer.LoadLatest(1); err != nil || h.WALOffset != 5 || string(payload) != "state-5" {
		t.Fatalf("peer h=%+v payload=%q err=%v", h, payload, err)
	}

	// A fresh owner with an empty local disk must recover from the peer.
	rs2, err := NewReplicaSetWithPeers([]string{t.TempDir()}, 2, []string{srv.URL}, "secret")
	if err != nil {
		t.Fatal(err)
	}
	h, payload, _, err := rs2.LoadLatest(1)
	if err != nil || h.WALOffset != 5 || string(payload) != "state-5" {
		t.Fatalf("load h=%+v payload=%q err=%v", h, payload, err)
	}
}

func TestPeerAuth(t *testing.T) {
	peer, _ := NewStore(t.TempDir())
	srv := httptest.NewServer(ServeReplica(peer, "secret"))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/internal/snapshot?shard=1")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestPeerQuorumFailure(t *testing.T) {
	peer, _ := NewStore(t.TempDir())
	srv := httptest.NewServer(ServeReplica(peer, "secret"))
	defer srv.Close()
	rs, _ := NewReplicaSetWithPeers([]string{t.TempDir()}, 2, []string{srv.URL}, "wrong-token")
	if _, err := rs.Save(1, 1, 1, []byte("x")); err == nil {
		t.Fatal("want quorum error")
	}
}

func TestStorePicksHighestOffset(t *testing.T) {
	s, _ := NewStore(t.TempDir())
	if _, err := s.Save(1, 1, 5, []byte("five")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Save(1, 1, 3, []byte("three")); err != nil {
		t.Fatal(err)
	}
	h, payload, _, err := s.LoadLatest(1)
	if err != nil || h.WALOffset != 5 || string(payload) != "five" {
		t.Fatalf("h=%+v payload=%q err=%v", h, payload, err)
	}
}
