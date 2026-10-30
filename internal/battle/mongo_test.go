package battle

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestMongoBattlePersists(t *testing.T) {
	uri := os.Getenv("MONGO_URL")
	if uri == "" {
		t.Skip("MONGO_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db := fmt.Sprintf("hapeserver_battle_%d", time.Now().UnixNano())
	store, err := OpenMongo(ctx, uri, db)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(store)
	b, err := svc.Submit(ctx, 1, 1, 10, 20, 900, 800)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(ctx); err != nil {
		t.Fatal(err)
	}

	store2, err := OpenMongo(ctx, uri, db)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = store2.coll.Drop(context.Background())
		_ = store2.Close(context.Background())
	}()
	got, err := store2.Get(ctx, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Winner != 10 || got.AttackerPower != 900 || got.State != Settled {
		t.Fatalf("battle = %+v", got)
	}
	hist, err := store2.ListByPlayer(ctx, 10, 1, 10)
	if err != nil || len(hist) != 1 || hist[0].ID != b.ID {
		t.Fatalf("history=%+v err=%v", hist, err)
	}
}
