// Package wal defines the write-ahead log contract and an in-memory
// implementation used by P0 tests. The JetStream implementation lands with
// task C02.
package wal

import (
	"sync"
)

// Log is an append-only sequence of opaque records.
type Log interface {
	Append(record []byte) (offset uint64, err error)
	Replay(fn func(offset uint64, record []byte) error) error
	Truncate(before uint64) error
	LastOffset() uint64
}

// Memory is a process-local Log.
type Memory struct {
	mu      sync.RWMutex
	base    uint64
	records [][]byte
}

// NewMemory returns an empty log.
func NewMemory() *Memory { return &Memory{} }

func (m *Memory) Append(record []byte) (uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]byte, len(record))
	copy(cp, record)
	m.records = append(m.records, cp)
	return m.base + uint64(len(m.records)), nil
}

func (m *Memory) Replay(fn func(offset uint64, record []byte) error) error {
	m.mu.RLock()
	cp := make([][]byte, len(m.records))
	copy(cp, m.records)
	m.mu.RUnlock()
	for i, r := range cp {
		if err := fn(m.base+uint64(i+1), r); err != nil {
			return err
		}
	}
	return nil
}

func (m *Memory) Truncate(before uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if before == 0 || before <= m.base+1 {
		return nil
	}
	last := m.base + uint64(len(m.records))
	if before > last+1 {
		before = last + 1
	}
	drop := before - 1 - m.base
	if drop > uint64(len(m.records)) {
		drop = uint64(len(m.records))
	}
	m.records = append([][]byte(nil), m.records[drop:]...)
	m.base = before - 1
	return nil
}

func (m *Memory) LastOffset() uint64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.base + uint64(len(m.records))
}
