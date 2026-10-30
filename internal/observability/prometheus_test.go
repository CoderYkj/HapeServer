package observability

import (
	"strings"
	"testing"
)

func TestPrometheus(t *testing.T) {
	m := New()
	m.Commands.Add(5)
	m.SetGauge("reconcile_last_touched", 3)
	out := m.Prometheus()
	if !strings.Contains(out, "# TYPE commands_total counter") || !strings.Contains(out, "commands_total 5") {
		t.Fatalf("missing counter:\n%s", out)
	}
	if !strings.Contains(out, "# TYPE reconcile_last_touched gauge") || !strings.Contains(out, "reconcile_last_touched 3") {
		t.Fatalf("missing gauge:\n%s", out)
	}
}
