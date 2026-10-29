package gateway

import "testing"

func TestDevToken(t *testing.T) {
	s, err := DevToken("dev:1:42")
	if err != nil {
		t.Fatal(err)
	}
	if s.WorldID != 1 || s.PlayerID != 42 {
		t.Fatalf("session = %+v", s)
	}
	for _, bad := range []string{"", "dev:1", "prod:1:2", "dev:x:2"} {
		if _, err := DevToken(bad); err == nil {
			t.Fatalf("token %q should fail", bad)
		}
	}
}
