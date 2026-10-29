package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"

	"hapeserver/internal/config"
	"hapeserver/internal/gateway"
	"hapeserver/internal/observability"
)

func main() {
	cfgPath := flag.String("config", "configs/default.json", "config file")
	addr := flag.String("addr", ":8080", "http listen address")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	metrics := observability.New()

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/metrics.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(metrics.Snapshot())
	})
	mux.HandleFunc("/session", func(w http.ResponseWriter, r *http.Request) {
		s, err := gateway.DevToken(r.Header.Get("Authorization"))
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(s)
	})

	log.Printf("gateway listening on %s (world=%d region=%s)", *addr, cfg.World.ID, cfg.World.Region)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatal(err)
	}
}
