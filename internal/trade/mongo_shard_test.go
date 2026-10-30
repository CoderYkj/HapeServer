package trade

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// TestMongoSharded runs the trade repo against a mongos router with three
// shards. It adds the shards and shards the trades collection on first use.
func TestMongoSharded(t *testing.T) {
	uri := os.Getenv("MONGO_SHARDED_URL")
	if uri == "" {
		t.Skip("MONGO_SHARDED_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Ping(ctx, nil); err != nil {
		t.Fatal(err)
	}
	admin := client.Database("admin")
	for i, host := range []string{"127.0.0.1:27101", "127.0.0.1:27102", "127.0.0.1:27103"} {
		name := fmt.Sprintf("shard%d", i+1)
		err := admin.RunCommand(ctx, bson.D{{Key: "addShard", Value: name + "/" + host}}).Err()
		if err != nil && !strings.Contains(err.Error(), "already") {
			t.Logf("addShard %s: %v", name, err)
		}
	}

	db := fmt.Sprintf("hapeserver_shard_%d", time.Now().UnixNano())
	if err := admin.RunCommand(ctx, bson.D{{Key: "enableSharding", Value: db}}).Err(); err != nil {
		t.Fatalf("enableSharding: %v", err)
	}
	if err := admin.RunCommand(ctx, bson.D{
		{Key: "shardCollection", Value: db + ".trades"},
		{Key: "key", Value: bson.D{{Key: "_id", Value: "hashed"}}},
	}).Err(); err != nil {
		t.Fatalf("shardCollection: %v", err)
	}

	repo, err := OpenMongo(ctx, uri, db)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = repo.coll.Drop(context.Background())
		_ = repo.Close(context.Background())
	}()

	tr := &Trade{
		ID:       fmt.Sprintf("t-%d", time.Now().UnixNano()),
		World:    7,
		Season:   1,
		A:        1,
		B:        2,
		State:    ALocked,
		Version:  1,
		ExpireAt: time.Now().Add(time.Minute).UnixMilli(),
	}
	if err := repo.Create(ctx, tr); err != nil {
		t.Fatal(err)
	}
	if err := repo.Transition(ctx, tr.ID, ALocked, BothLocked, 2); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(ctx, tr.ID)
	if err != nil || got.State != BothLocked {
		t.Fatalf("get: %+v err=%v", got, err)
	}
	pending, err := repo.ListPending(ctx, 10)
	if err != nil || len(pending) == 0 {
		t.Fatalf("pending=%d err=%v", len(pending), err)
	}

	for i := 0; i < 100; i++ {
		extra := &Trade{
			ID:       fmt.Sprintf("t-%d-%d", time.Now().UnixNano(), i),
			World:    7,
			Season:   1,
			A:        1,
			B:        2,
			State:    ALocked,
			Version:  1,
			ExpireAt: time.Now().Add(time.Minute).UnixMilli(),
		}
		if err := repo.Create(ctx, extra); err != nil {
			t.Fatal(err)
		}
	}

	var coll bson.M
	if err := client.Database("config").Collection("collections").
		FindOne(ctx, bson.M{"_id": db + ".trades"}).Decode(&coll); err != nil {
		t.Fatal(err)
	}
	if coll["dropped"] == true || coll["key"] == nil {
		t.Fatalf("collection not sharded: %+v", coll)
	}

	cur, err := client.Database("config").Collection("chunks").
		Find(ctx, bson.M{"uuid": coll["uuid"]})
	if err != nil {
		t.Fatal(err)
	}
	var chunks []bson.M
	if err := cur.All(ctx, &chunks); err != nil {
		t.Fatal(err)
	}
	shards := make(map[string]bool)
	for _, c := range chunks {
		if s, ok := c["shard"].(string); ok {
			shards[s] = true
		}
	}
	t.Logf("chunks=%d shards=%v", len(chunks), shards)
	if len(shards) < 2 {
		t.Fatalf("trades only on %d shard(s): %v", len(shards), shards)
	}
}
