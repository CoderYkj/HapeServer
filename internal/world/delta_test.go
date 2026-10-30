package world

import "testing"

func TestDeltaFieldLevel(t *testing.T) {
	base := PlayerState{PlayerID: 1, Version: 1, Resources: Assets{Gold: 100, Wood: 50}, City: CityState{Level: 1}, Power: 10}
	cur := base
	cur.Version = 2
	cur.Resources = Assets{Gold: 150, Wood: 50}
	cur.Power = 20
	s := &Service{
		players:   map[uint64]*PlayerActor{1: {State: cur}},
		stateHist: map[uint64][]PlayerState{1: {cloneState(base), cloneState(cur)}},
	}
	d := s.Delta(1, 1)
	if d == nil || d.StateVersion != 2 {
		t.Fatalf("delta = %+v", d)
	}
	if len(d.Changed) != 2 {
		t.Fatalf("changed = %v", d.Changed)
	}
	if d.Resources == nil || d.Resources.Gold != 150 || d.Power != 20 {
		t.Fatalf("delta values = %+v", d)
	}
	if d.City != nil {
		t.Fatalf("city should be unchanged: %+v", d.City)
	}
}

func TestDecodeRejectsLegacyJSON(t *testing.T) {
	if _, err := decodeStates([]byte(`[]`)); err == nil {
		t.Fatal("legacy JSON snapshot must be rejected")
	}
}
