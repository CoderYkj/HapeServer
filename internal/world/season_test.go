package world

import (
	"errors"
	"testing"
)

func TestResetSeason(t *testing.T) {
	p := NewPlayer(1, 1)
	p.State.Resources = Assets{Gold: 500, Wood: 400, Food: 300}
	p.State.Power = 999
	p.State.City.Level = 7
	p.State.Marches = []March{{ID: 1}}
	p.State.Queues = []Queue{{ID: 1, Kind: "build"}}
	p.LockEscrow("t1", Assets{Gold: 10})
	if err := p.resetSeason(2); !errors.Is(err, ErrActiveEscrow) {
		t.Fatalf("want ErrActiveEscrow, got %v", err)
	}
	if err := p.RefundEscrow("t1"); err != nil {
		t.Fatal(err)
	}
	if err := p.resetSeason(2); err != nil {
		t.Fatal(err)
	}
	if p.State.SeasonID != 2 || p.State.Power != 0 || p.State.City.Level != 0 {
		t.Fatalf("state = %+v", p.State)
	}
	if p.State.Resources != (Assets{Gold: 1000, Wood: 1000, Food: 1000}) {
		t.Fatalf("resources = %+v", p.State.Resources)
	}
	if p.State.Marches != nil || p.State.Queues != nil {
		t.Fatalf("marches/queues not cleared: %+v %+v", p.State.Marches, p.State.Queues)
	}
}

func TestSeasonReward(t *testing.T) {
	cases := map[int]int64{1: 5000, 2: 2000, 10: 2000, 11: 1000, 100: 1000, 101: 500, 1000: 500, 1001: 100}
	for rank, want := range cases {
		if got := seasonReward(rank); got != want {
			t.Fatalf("rank %d: got %d want %d", rank, got, want)
		}
	}
}
