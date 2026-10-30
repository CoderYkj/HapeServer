package trade

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

// TestMongoRepoPersistsAcrossReconnect verifies the trade document survives a
// client reconnect (i.e. it is really in MongoDB, not in process memory).
func TestMongoRepoPersistsAcrossReconnect(t *testing.T) {
	uri := os.Getenv("MONGO_URL")
	if uri == "" {
		t.Skip("MONGO_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	db := fmt.Sprintf("hapeserver_persist_%d", time.Now().UnixNano())
	repo, err := OpenMongo(ctx, uri, db)
	if err != nil {
		t.Fatal(err)
	}
	tr := &Trade{
		ID:       fmt.Sprintf("t-%d", time.Now().UnixNano()),
		World:    1,
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
	if err := repo.Close(ctx); err != nil {
		t.Fatal(err)
	}

	repo2, err := OpenMongo(ctx, uri, db)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = repo2.coll.Drop(context.Background())
		_ = repo2.Close(context.Background())
	}()
	got, err := repo2.Get(ctx, tr.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != BothLocked || got.Version != 2 {
		t.Fatalf("got state=%s version=%d", got.State, got.Version)
	}
}