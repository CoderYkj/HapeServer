package main

import (
	"flag"
	"log"
	"time"
)

func main() {
	n := flag.Int("n", 1000, "number of bots")
	rate := flag.Float64("rate", 0.2, "commands per bot per second")
	duration := flag.Duration("duration", time.Hour, "test duration")
	target := flag.String("target", "ws://127.0.0.1:8080/ws", "gateway endpoint")
	flag.Parse()

	if *n <= 0 || *rate <= 0 || *duration <= 0 {
		log.Fatal("n, rate and duration must be positive")
	}

	log.Printf("bot plan: n=%d rate=%.2f/s duration=%s target=%s total_cmds~%.0f",
		*n, *rate, *duration, *target, float64(*n)*(*rate)*duration.Seconds())

	// P0 H1: dial the gateway, authenticate with a dev token, then execute the
	// scenario mix (build, march, trade, rank query). The load generator lands
	// with task H03/H04.
	time.Sleep(*duration)
}
