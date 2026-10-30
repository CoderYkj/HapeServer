package link

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func startEcho(t *testing.T, h Handler) *Server {
	t.Helper()
	srv, err := Listen("127.0.0.1:0", h)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(context.Background()) }()
	t.Cleanup(func() { _ = srv.Close() })
	return srv
}

func TestCallMultiplexed(t *testing.T) {
	srv := startEcho(t, func(_ context.Context, p []byte) ([]byte, error) {
		return append([]byte("echo:"), p...), nil
	})
	c, err := Dial(srv.Addr(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			in := []byte(fmt.Sprintf("%d", i))
			out, err := c.Call(context.Background(), in)
			if err != nil {
				t.Errorf("call %d: %v", i, err)
				return
			}
			if string(out) != "echo:"+string(in) {
				t.Errorf("call %d: %q", i, out)
			}
		}(i)
	}
	wg.Wait()
}

func TestCallError(t *testing.T) {
	srv := startEcho(t, func(context.Context, []byte) ([]byte, error) {
		return nil, errors.New("boom")
	})
	c, err := Dial(srv.Addr(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if _, err := c.Call(context.Background(), []byte("x")); err == nil || err.Error() != "boom" {
		t.Fatalf("err = %v", err)
	}
}

func TestCallTimeout(t *testing.T) {
	srv := startEcho(t, func(ctx context.Context, _ []byte) ([]byte, error) {
		time.Sleep(100 * time.Millisecond)
		return []byte("late"), nil
	})
	c, _ := Dial(srv.Addr(), time.Second)
	defer func() { _ = c.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := c.Call(ctx, []byte("x")); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
}
