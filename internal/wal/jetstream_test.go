package wal

import (
	"fmt"
	"os"
	"testing"
	"time"
)

// TestJetStreamRoundTrip requires a running NATS server with JetStream.
// Set NATS_URL (e.g. nats://127.0.0.1:4222) to run it; otherwise it skips so
// the default unit-test run stays hermetic.
func TestJetStreamRoundTrip(t *testing.T) {
	url := os.Getenv("NATS_URL")
	if url == "" {
		t.Skip("NATS_URL not set")
	}
	suffix := time.Now().UnixNano()
	stream := fmt.Sprintf("WAL_TEST_%d", suffix)
	subject := fmt.Sprintf("wal.test.%d", suffix)

	replicas := 1
	if os.Getenv("NATS_REPLICAS") == "3" {
		replicas = 3
	}
	w, err := OpenJetStream(JetStreamConfig{URL: url, Stream: stream, Subject: subject, Replicas: replicas})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()
	info, err := w.js.StreamInfo(stream)
	if err != nil {
		t.Fatal(err)
	}
	if info.Config.Replicas != replicas {
		t.Fatalf("stream replicas = %d, want %d", info.Config.Replicas, replicas)
	}

	for _, s := range []string{"a", "b", "c"} {
		if _, err := w.Append([]byte(s)); err != nil {
			t.Fatal(err)
		}
	}
	if got := w.LastOffset(); got != 3 {
		t.Fatalf("last offset = %d, want 3", got)
	}

	var records []string
	var offsets []uint64
	if err := w.Replay(func(off uint64, r []byte) error {
		offsets = append(offsets, off)
		records = append(records, string(r))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(records) != 3 || records[0] != "a" || records[2] != "c" {
		t.Fatalf("records = %v", records)
	}
	if len(offsets) != 3 || offsets[0] != 1 || offsets[2] != 3 {
		t.Fatalf("offsets = %v", offsets)
	}

	if err := w.Truncate(2); err != nil {
		t.Fatal(err)
	}
	records = nil
	offsets = nil
	if err := w.Replay(func(off uint64, r []byte) error {
		offsets = append(offsets, off)
		records = append(records, string(r))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || records[0] != "b" {
		t.Fatalf("after truncate = %v", records)
	}
	if len(offsets) != 2 || offsets[0] != 2 || offsets[1] != 3 {
		t.Fatalf("offsets after truncate = %v", offsets)
	}
	if got := w.LastOffset(); got != 3 {
		t.Fatalf("last offset after truncate = %d", got)
	}
}
