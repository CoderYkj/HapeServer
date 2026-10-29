package shard

import "testing"

func TestStableAndDistinct(t *testing.T) {
	a := OfPlayer(1, 100)
	b := OfPlayer(1, 100)
	if a != b {
		t.Fatal("player shard is not stable")
	}
	if OfPlayer(1, 100) == OfPlayer(1, 101) {
		t.Fatal("expected different shards for different players")
	}
	if OfRegion(1, 0, 0) == OfRegion(1, 1, 0) {
		t.Fatal("expected different shards for different regions")
	}
}
