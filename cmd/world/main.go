package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"hapeserver/internal/battle"
	"hapeserver/internal/config"
	"hapeserver/internal/link"
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
	tcpAddr := flag.String("tcp", ":9000", "internal TCP listen address")
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

	dirs := cfg.Storage.SnapshotDirs
	if len(dirs) == 0 {
		dirs = []string{cfg.Storage.SnapshotDir}
	}
	snapSet, err := snapshot.NewReplicaSetWithPeers(dirs, cfg.Storage.SnapshotQuorum, cfg.Storage.SnapshotPeers, cfg.Storage.ReplicaToken)
	if err != nil {
		log.Fatalf("snapshot store: %v", err)
	}
	log.Printf("snapshot replicas=%d quorum=%d peers=%d", snapSet.Replicas(), snapSet.Quorum(), len(cfg.Storage.SnapshotPeers))

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

	var tradeStore trade.Store = trade.NewMemoryStore()
	if cfg.Storage.MongoURL != "" {
		repo, err := trade.OpenMongo(ctx, cfg.Storage.MongoURL, cfg.Storage.MongoDB)
		if err != nil {
			log.Fatalf("mongo trade store: %v", err)
		}
		defer func() { _ = repo.Close(context.Background()) }()
		tradeStore = repo
		log.Printf("trade store: mongodb db=%s", cfg.Storage.MongoDB)
	} else {
		log.Printf("trade store: memory (durability is dev-only)")
	}

	svc := world.NewService(sys, tradeStore, walLog)
	svc.SetAlertHook(func(errs []string) {
		log.Printf("ALERT reconcile failures=%d first=%s", len(errs), errs[0])
	})
	svc.SetSeason(cfg.World.Season)
	if len(cfg.Season.Rewards) > 0 {
		tiers := make([]world.RewardTier, 0, len(cfg.Season.Rewards))
		for _, r := range cfg.Season.Rewards {
			tiers = append(tiers, world.RewardTier{MinRank: r.MinRank, MaxRank: r.MaxRank, Gold: r.Gold})
		}
		svc.SetRewards(tiers)
	}
	if cfg.Storage.MongoURL != "" {
		bs, err := battle.OpenMongo(ctx, cfg.Storage.MongoURL, cfg.Storage.MongoDB)
		if err != nil {
			log.Fatalf("mongo battle store: %v", err)
		}
		defer func() { _ = bs.Close(context.Background()) }()
		svc.UseBattleStore(bs)
		log.Printf("battle store: mongodb db=%s", cfg.Storage.MongoDB)
	}
	last, err := recovery.Recover(snapSet, walLog, cfg.World.ID, svc)
	if err != nil {
		log.Fatalf("recover: %v", err)
	}
	log.Printf("recovered shard=%d wal_offset=%d", cfg.World.ID, last)
	if reset, skipped := svc.SweepSeason(ctx, cfg.World.Season); reset > 0 || skipped > 0 {
		log.Printf("season sweep: reset=%d skipped=%d", reset, skipped)
	}

	tcpSrv, err := link.Listen(*tcpAddr, world.NewTCPHandler(svc))
	if err != nil {
		log.Fatalf("tcp listen: %v", err)
	}
	defer func() { _ = tcpSrv.Close() }()
	go func() {
		if err := tcpSrv.Serve(ctx); err != nil {
			log.Printf("tcp server stopped: %v", err)
		}
	}()
	log.Printf("world tcp listening on %s", tcpSrv.Addr())

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/api/reconcile/errors", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"errors": svc.ReconcileErrors()})
	})
	if cfg.Storage.ReplicaToken != "" {
		mux.HandleFunc("/internal/snapshot", snapshot.ServeReplica(snapSet.Primary(), cfg.Storage.ReplicaToken))
	}
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(svc.Metrics().Snapshot())
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
	if !cfg.Storage.DisableSnapshot {
		go snapshotLoop(ctx, stop, snapSet, svc, walLog, cfg.World.ID)
	}
	go reconcileLoop(ctx, stop, svc)
	go seasonLoop(ctx, stop, svc)

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
	if !cfg.Storage.DisableSnapshot {
		if err := saveSnapshot(snapSet, walLog, cfg.World.ID, svc); err != nil {
			log.Printf("final snapshot: %v", err)
		}
	}
}

func saveSnapshot(set snapshot.Set, log wal.Log, shard uint32, svc *world.Service) error {
	payload, offset, err := svc.SnapshotWithOffset()
	if err != nil {
		return err
	}
	if _, err := set.Save(shard, 1, offset, payload); err != nil {
		return err
	}
	if err := log.Truncate(offset + 1); err != nil {
		return fmt.Errorf("wal truncate: %w", err)
	}
	return nil
}

func snapshotLoop(ctx context.Context, stop <-chan struct{}, snapSet snapshot.Set, svc *world.Service, walLog wal.Log, shard uint32) {
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			if err := saveSnapshot(snapSet, walLog, shard, svc); err != nil {
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

func reconcileLoop(ctx context.Context, stop <-chan struct{}, svc *world.Service) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			n, err := svc.Reconcile(cctx, 100)
			cancel()
			if err != nil {
				log.Printf("reconcile: %v", err)
			} else if n > 0 {
				log.Printf("reconcile: touched=%d", n)
			}
		}
	}
}

func seasonLoop(ctx context.Context, stop <-chan struct{}, svc *world.Service) {
	t := time.NewTicker(24 * time.Hour)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			if reset, skipped := svc.SweepSeason(ctx, svc.CurrentSeason()); reset > 0 || skipped > 0 {
				log.Printf("season sweep: reset=%d skipped=%d", reset, skipped)
			}
		}
	}
}
