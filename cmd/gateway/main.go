package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"strconv"

	"hapeserver/internal/gateway"
	"hapeserver/internal/link"
)

func main() {
	addr := flag.String("addr", ":9001", "player TCP listen address")
	admin := flag.String("admin", ":8081", "admin HTTP listen address")
	worldTCP := flag.String("world-tcp", "127.0.0.1:9000", "world TCP endpoint")
	pool := flag.Int("tcp-pool", 4, "world TCP connections per gateway")
	flag.Parse()

	remote := gateway.NewTCPRemote(*worldTCP, *pool)
	srv, err := link.ListenPlayer(*addr, gateway.NewPlayerConnHandler(remote))
	if err != nil {
		log.Fatalf("player tcp listen: %v", err)
	}
	defer func() { _ = srv.Close() }()
	go func() {
		if err := srv.Serve(context.Background()); err != nil {
			log.Printf("player tcp stopped: %v", err)
		}
	}()
	log.Printf("gateway tcp listening on %s -> world %s (pool=%d)", *addr, *worldTCP, *pool)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/admin/pool", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			n, err := strconv.Atoi(r.URL.Query().Get("size"))
			if err != nil {
				http.Error(w, "bad size", http.StatusBadRequest)
				return
			}
			remote.SetSize(n)
		}
		_, _ = fmt.Fprintf(w, "pool=%d\n", remote.Size())
	})
	log.Printf("gateway admin listening on %s", *admin)
	if err := http.ListenAndServe(*admin, mux); err != nil {
		log.Fatal(err)
	}
}
