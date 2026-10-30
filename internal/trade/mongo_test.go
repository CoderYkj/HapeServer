package trade

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"
)

// TestMongoRepo requires MONGO_URL, e.g. mongodb://127.0.0.1:27017.
func TestMongoRepo(t *testing.T) {
	uri := os.Getenv("MONGO_URL")
	if uri == "" {
		t.Skip("MONGO_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	repo, err := OpenMongo(ctx, uri, "hapeserver_test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = repo.Close(context.Background()) }()

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
	got, err := repo.Get(ctx, tr.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != ALocked || got.Version != 1 {
		t.Fatalf("got %+v", got)
	}

	if err := repo.Transition(ctx, tr.ID, ALocked, BothLocked, 2); err != nil {
		t.Fatal(err)
	}
	if err := repo.Transition(ctx, tr.ID, ALocked, BothLocked, 2); !errors.Is(err, ErrConflict) {
		t.Fatalf("want conflict, got %v", err)
	}
	if err := repo.Transition(ctx, tr.ID, BothLocked, Settling, 3); err != nil {
		t.Fatal(err)
	}
	if err := repo.Transition(ctx, tr.ID, Settling, Settled, 4); err != nil {
		t.Fatal(err)
	}

	pending, err := repo.ListPending(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pending {
		if p.ID == tr.ID {
			t.Fatalf("settled trade still pending: %+v", p)
		}
	}
}
