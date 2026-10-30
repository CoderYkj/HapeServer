package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"hapeserver/internal/config"
	"hapeserver/internal/recovery"
	"hapeserver/internal/runtime"
	"hapeserver/internal/snapshot"
	"hapeserver/internal/trade"
	"hapeserver/internal/wal"
	"hapeserver/internal/world"
)

type apiResponse struct {
	OK    bool   `json:"ok"`
	Data  any    `json:"data,omitempty"`
	Error string `json:"error,omitempty"`
}

func main() {
	cfgPath := flag.String("config", "configs/default.json", "config file")
	addr := flag.String("addr", ":8080", "HTTP listen address")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	sys, err := runtime.New(cfg.Runtime)
	if err != nil {
		log.Fatalf("runtime: %v", err)
	}
	ctx := context.Background()
	if err := sys.Start(ctx); err != nil {
		log.Fatalf("start: %v", err)
	}
	defer func() { _ = sys.Stop(ctx) }()

	store, err := snapshot.NewStore(cfg.Storage.SnapshotDir)
	if err != nil {
		log.Fatalf("snapshot store: %v", err)
	}

	var walLog wal.Log
	switch cfg.Storage.WALKind {
	case "jetstream":
		js, err := wal.OpenJetStream(wal.JetStreamConfig{
			URL:     cfg.Storage.NATSURL,
			Stream:  cfg.Storage.WALStream,
			Subject: cfg.Storage.WALSubject,
		})
		if err != nil {
			log.Fatalf("jetstream wal: %v", err)
		}
		walLog = js
		defer func() { _ = js.Close() }()
	default:
		walLog = wal.NewMemory()
	}

	svc := world.NewService(sys, trade.NewMemoryStore(), walLog)
	last, err := recovery.Recover(store, walLog, cfg.World.ID, svc)
	if err != nil {
		log.Fatalf("recover: %v", err)
	}
	log.Printf("recovered shard=%d wal_offset=%d", cfg.World.ID, last)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/api/command", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req world.Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		data, err := svc.Handle(r.Context(), req)
		w.Header().Set("Content-Type", "application/json")
		resp := apiResponse{OK: err == nil, Data: data}
		if err != nil {
			resp.Error = err.Error()
			w.WriteHeader(http.StatusBadRequest)
		}
		_ = json.NewEncoder(w).Encode(resp)
	})
	mux.HandleFunc("/api/rank", func(w http.ResponseWriter, r *http.Request) {
		var pid uint64
		_, _ = fmtSscan(r.URL.Query().Get("player"), &pid)
		rankN, ok := svc.Rank().RankOf(pid)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": ok, "rank": rankN})
	})

	stop := make(chan struct{})
	go snapshotLoop(ctx, stop, store, svc, walLog, cfg.World.ID)

	srv := &http.Server{Addr: *addr, Handler: mux}
	go func() {
		log.Printf("world %d listening on %s", cfg.World.ID, *addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("http: %v", err)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	close(stop)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	if payload, err := svc.Snapshot(); err == nil {
		if _, err := store.Save(cfg.World.ID, 1, walLog.LastOffset(), payload); err != nil {
			log.Printf("final snapshot: %v", err)
		}
	}
}

func snapshotLoop(ctx context.Context, stop <-chan struct{}, store *snapshot.Store, svc *world.Service, walLog wal.Log, shard uint32) {
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			payload, err := svc.Snapshot()
			if err != nil {
				continue
			}
			if _, err := store.Save(shard, 1, walLog.LastOffset(), payload); err != nil {
				log.Printf("snapshot: %v", err)
			}
			_ = ctx
		}
	}
}

func fmtSscan(s string, v *uint64) (int, error) {
	var n uint64
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, errors.New("bad number")
		}
		n = n*10 + uint64(c-'0')
	}
	*v = n
	return 1, nil
}
