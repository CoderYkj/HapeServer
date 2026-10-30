package trade

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
)

func TestMongoIndexes(t *testing.T) {
	uri := os.Getenv("MONGO_URL")
	if uri == "" {
		t.Skip("MONGO_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db := fmt.Sprintf("hapeserver_idx_%d", time.Now().UnixNano())
	repo, err := OpenMongo(ctx, uri, db)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = repo.coll.Drop(context.Background())
		_ = repo.Close(context.Background())
	}()

	cur, err := repo.coll.Indexes().List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var indexes []bson.M
	if err := cur.All(ctx, &indexes); err != nil {
		t.Fatal(err)
	}
	byName := make(map[string]bson.M, len(indexes))
	for _, ix := range indexes {
		if name, ok := ix["name"].(string); ok {
			byName[name] = ix
		}
	}
	if _, ok := byName["state_expireAt"]; !ok {
		t.Fatalf("missing state_expireAt, got %v", byName)
	}
	ttl, ok := byName["ttl_expireDate"]
	if !ok {
		t.Fatalf("missing ttl_expireDate, got %v", byName)
	}
	if v, ok := ttl["expireAfterSeconds"]; !ok || v.(int32) != 0 {
		t.Fatalf("ttl expireAfterSeconds = %v", ttl["expireAfterSeconds"])
	}
}
