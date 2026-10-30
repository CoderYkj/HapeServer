package main

import (
	"flag"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"

	"hapeserver/internal/gateway"
)

func main() {
	worldURL := flag.String("world", "http://127.0.0.1:8080", "world HTTP base URL")
	addr := flag.String("addr", ":8081", "gateway listen address")
	flag.Parse()

	target, err := url.Parse(*worldURL)
	if err != nil {
		log.Fatalf("world url: %v", err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	remote := gateway.NewRemote(*worldURL)

	mux := http.NewServeMux()
	mux.Handle("/ws", gateway.NewWS(remote))
	mux.Handle("/api/command", proxy)
	mux.Handle("/api/rank", proxy)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	log.Printf("gateway listening on %s -> %s", *addr, *worldURL)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatal(err)
	}
}
