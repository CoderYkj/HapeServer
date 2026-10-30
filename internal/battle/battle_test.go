package battle

import (
	"context"
	"testing"
)

func TestResolve(t *testing.T) {
	if got := Resolve(1, 2, 100, 90); got != 1 {
		t.Fatalf("attacker win: %d", got)
	}
	if got := Resolve(1, 2, 90, 100); got != 2 {
		t.Fatalf("defender win: %d", got)
	}
	if got := Resolve(1, 2, 100, 100); got != 2 {
		t.Fatalf("tie should favor defender: %d", got)
	}
}

func TestSubmitAndGet(t *testing.T) {
	svc := NewService(nil)
	b, err := svc.Submit(context.Background(), 1, 1, 10, 20, 500, 400)
	if err != nil {
		t.Fatal(err)
	}
	if b.Winner != 10 || b.State != Settled {
		t.Fatalf("battle = %+v", b)
	}
	got, err := svc.Get(context.Background(), b.ID)
	if err != nil || got.Winner != 10 {
		t.Fatalf("get: %+v err=%v", got, err)
	}
}

func TestHistory(t *testing.T) {
	svc := NewService(nil)
	if _, err := svc.Submit(context.Background(), 1, 1, 10, 20, 100, 50); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Submit(context.Background(), 1, 2, 10, 20, 100, 50); err != nil {
		t.Fatal(err)
	}
	allPage, err := svc.History(context.Background(), 10, 0, 0, "", 10)
	if err != nil || len(allPage.Items) != 2 {
		t.Fatalf("all=%d err=%v", len(allPage.Items), err)
	}
	season1Page, err := svc.History(context.Background(), 10, 1, 0, "", 10)
	if err != nil || len(season1Page.Items) != 1 || season1Page.Items[0].Season != 1 {
		t.Fatalf("season1=%+v err=%v", season1Page.Items, err)
	}
}

func TestHistoryPagination(t *testing.T) {
	svc := NewService(nil)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, err := svc.Submit(ctx, 1, 1, 10, 20, 100, 50); err != nil {
			t.Fatal(err)
		}
	}
	page1, err := svc.History(ctx, 10, 1, 0, "", 2)
	if err != nil || len(page1.Items) != 2 || page1.NextID == "" {
		t.Fatalf("page1=%+v err=%v", page1, err)
	}
	page2, err := svc.History(ctx, 10, 1, page1.NextCreatedAt, page1.NextID, 2)
	if err != nil || len(page2.Items) != 1 {
		t.Fatalf("page2=%+v err=%v", page2, err)
	}
}
