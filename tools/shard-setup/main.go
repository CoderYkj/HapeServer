// Command shard-setup initiates the single-node replica sets used by the
// local 3-shard MongoDB integration environment.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func main() {
	config := flag.String("config", "127.0.0.1:27100", "config server host:port")
	shards := flag.String("shards", "127.0.0.1:27101,127.0.0.1:27102,127.0.0.1:27103", "shard host:port list")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := ensureRS(ctx, *config, "cfg"); err != nil {
		log.Fatalf("config rs: %v", err)
	}
	for i, hp := range strings.Split(*shards, ",") {
		name := fmt.Sprintf("shard%d", i+1)
		if err := ensureRS(ctx, strings.TrimSpace(hp), name); err != nil {
			log.Fatalf("%s: %v", name, err)
		}
	}
	fmt.Println("replica sets ready")
}

func ensureRS(ctx context.Context, host, name string) error {
	uri := "mongodb://" + host + "/?directConnection=true"
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		return err
	}
	defer func() { _ = client.Disconnect(context.Background()) }()

	admin := client.Database("admin")
	if err := admin.RunCommand(ctx, bson.D{{Key: "replSetGetStatus", Value: 1}}).Err(); err != nil {
		cfg := bson.D{
			{Key: "_id", Value: name},
			{Key: "members", Value: bson.A{bson.D{
				{Key: "_id", Value: 0},
				{Key: "host", Value: host},
			}}},
		}
		if err := admin.RunCommand(ctx, bson.D{{Key: "replSetInitiate", Value: cfg}}).Err(); err != nil {
			return fmt.Errorf("initiate: %w", err)
		}
	}
	for i := 0; i < 60; i++ {
		var hello bson.M
		if err := admin.RunCommand(ctx, bson.D{{Key: "hello", Value: 1}}).Decode(&hello); err == nil {
			if p, _ := hello["isWritablePrimary"].(bool); p {
				return nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("no primary after 30s")
}
