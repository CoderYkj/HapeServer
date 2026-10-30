package observability

import (
	"fmt"
	"sort"
	"strings"
)

// Prometheus renders the current counters/gauges in text exposition format.
func (m *Metrics) Prometheus() string {
	snap := m.Snapshot()
	keys := make([]string, 0, len(snap))
	for k := range snap {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		typ := "gauge"
		if strings.HasSuffix(k, "_total") {
			typ = "counter"
		}
		fmt.Fprintf(&b, "# TYPE %s %s\n%s %d\n", k, typ, k, snap[k])
	}
	return b.String()
}
