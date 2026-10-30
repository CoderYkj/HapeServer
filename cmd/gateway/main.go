package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"

	"hapeserver/internal/gateway"
)

func main() {
	worldURL := flag.String("world", "http://127.0.0.1:8080", "world HTTP base URL")
	worldTCP := flag.String("world-tcp", "", "world TCP endpoint (empty = HTTP)")
	tcpPool := flag.Int("tcp-pool", 4, "TCP connections per world")
	addr := flag.String("addr", ":8081", "gateway listen address")
	flag.Parse()

	target, err := url.Parse(*worldURL)
	if err != nil {
		log.Fatalf("world url: %v", err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	var handler gateway.Handler
	if *worldTCP != "" {
		handler = gateway.NewTCPRemote(*worldTCP, *tcpPool)
	} else {
		handler = gateway.NewRemote(*worldURL)
	}

	mux := http.NewServeMux()
	mux.Handle("/ws", gateway.NewWS(handler))
	mux.Handle("/api/command", proxy)
	mux.Handle("/api/rank", proxy)
	mux.HandleFunc("/admin/pool", func(w http.ResponseWriter, r *http.Request) {
		remote, ok := handler.(*gateway.TCPRemote)
		if !ok {
			http.Error(w, "tcp pool not enabled", http.StatusBadRequest)
			return
		}
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
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	log.Printf("gateway listening on %s -> %s (tcp=%q)", *addr, *worldURL, *worldTCP)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatal(err)
	}
}
