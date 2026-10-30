package snapshot

import (
	"bytes"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

const replicaTokenHeader = "X-Replica-Token"

type peerReplica struct {
	base  string
	token string
	hc    *http.Client
}

func newPeerReplica(base, token string) *peerReplica {
	return &peerReplica{base: base, token: token, hc: &http.Client{Timeout: 5 * time.Second}}
}

func (p *peerReplica) save(body []byte) error {
	req, err := http.NewRequest(http.MethodPost, p.base+"/internal/snapshot", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set(replicaTokenHeader, p.token)
	resp, err := p.hc.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("peer save %s: %d %s", p.base, resp.StatusCode, msg)
	}
	return nil
}

func (p *peerReplica) load(shard uint32) (Header, []byte, error) {
	u := fmt.Sprintf("%s/internal/snapshot?shard=%d", p.base, shard)
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return Header{}, nil, err
	}
	req.Header.Set(replicaTokenHeader, p.token)
	resp, err := p.hc.Do(req)
	if err != nil {
		return Header{}, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return Header{}, nil, ErrNoSnapshot
	}
	if resp.StatusCode != http.StatusOK {
		return Header{}, nil, fmt.Errorf("peer load %s: %d", p.base, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return Header{}, nil, err
	}
	h, payload, err := Decode(body)
	if err != nil {
		return Header{}, nil, err
	}
	return h, payload, nil
}

// ServeReplica exposes a local store as a peer replica endpoint.
func ServeReplica(store *Store, token string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get(replicaTokenHeader)
		if token == "" || subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		switch r.Method {
		case http.MethodPost:
			body, err := io.ReadAll(io.LimitReader(r.Body, 64<<20))
			if err != nil {
				http.Error(w, "read body", http.StatusBadRequest)
				return
			}
			h, payload, err := Decode(body)
			if err != nil {
				http.Error(w, "bad snapshot", http.StatusBadRequest)
				return
			}
			if _, err := store.Save(h.Shard, h.Epoch, h.WALOffset, payload); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			shard, err := strconv.ParseUint(r.URL.Query().Get("shard"), 10, 32)
			if err != nil {
				http.Error(w, "bad shard", http.StatusBadRequest)
				return
			}
			h, payload, _, err := store.LoadLatest(uint32(shard))
			if errors.Is(err, ErrNoSnapshot) {
				http.NotFound(w, r)
				return
			}
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write(Encode(h, payload))
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
}
