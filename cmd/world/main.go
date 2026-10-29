package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"hapeserver/internal/config"
	"hapeserver/internal/runtime"
	"hapeserver/internal/world"
)

func main() {
	cfgPath := flag.String("config", "configs/default.json", "config file")
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

	player, err := sys.Spawn(ctx, "player:1", world.NewPlayer(1, 1))
	if err != nil {
		log.Fatalf("spawn player: %v", err)
	}
	region, err := sys.Spawn(ctx, "region:0:0", world.NewRegion(cfg.World.ID, 0, 0))
	if err != nil {
		log.Fatalf("spawn region: %v", err)
	}

	log.Printf("world %d started (region=%s shards=%d) player=%v region=%v",
		cfg.World.ID, cfg.World.Region, cfg.World.Shards, player, region)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig

	_ = sys.Stop(ctx)
}
