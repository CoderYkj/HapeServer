// Package observability exposes the small metric surface P0 needs. A
// Prometheus exporter replaces the snapshot endpoint in P1.
package observability

import (
	"sync"
	"sync/atomic"
)

// Metrics is a process-wide counter set.
type Metrics struct {
	Commands         atomic.Uint64
	Errors           atomic.Uint64
	TradeOK          atomic.Uint64
	TradeFail        atomic.Uint64
	RankLookup       atomic.Uint64
	ReconcileRuns    atomic.Uint64
	ReconcileTouched atomic.Uint64
	ReconcileErrors  atomic.Uint64

	mu     sync.RWMutex
	gauges map[string]int64
}

// New returns empty metrics.
func New() *Metrics { return &Metrics{gauges: make(map[string]int64)} }

// SetGauge records a point-in-time value.
func (m *Metrics) SetGauge(name string, v int64) {
	m.mu.Lock()
	m.gauges[name] = v
	m.mu.Unlock()
}

// Snapshot copies counters and gauges for reporting.
func (m *Metrics) Snapshot() map[string]int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]int64, len(m.gauges)+8)
	for k, v := range m.gauges {
		out[k] = v
	}
	out["commands_total"] = int64(m.Commands.Load())
	out["errors_total"] = int64(m.Errors.Load())
	out["trade_ok_total"] = int64(m.TradeOK.Load())
	out["trade_fail_total"] = int64(m.TradeFail.Load())
	out["rank_lookup_total"] = int64(m.RankLookup.Load())
	out["reconcile_runs_total"] = int64(m.ReconcileRuns.Load())
	out["reconcile_touched_total"] = int64(m.ReconcileTouched.Load())
	out["reconcile_errors_total"] = int64(m.ReconcileErrors.Load())
	return out
}
