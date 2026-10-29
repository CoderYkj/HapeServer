package snapshot

import (
	"errors"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	h := Header{Version: 1, Shard: 7, Epoch: 3, WALOffset: 42, Count: 2}
	b := Encode(h, []byte("payload"))

	got, payload, err := Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	if got != h {
		t.Fatalf("header = %+v, want %+v", got, h)
	}
	if string(payload) != "payload" {
		t.Fatalf("payload = %q", payload)
	}
}

func TestCorruptDetected(t *testing.T) {
	b := Encode(Header{Version: 1}, []byte("payload"))
	b[len(b)-1] ^= 0xff
	if _, _, err := Decode(b); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("want ErrCorrupt, got %v", err)
	}
}
