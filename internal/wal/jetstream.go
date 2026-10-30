package wal

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
)

// JetStreamConfig identifies one shard's WAL stream.
type JetStreamConfig struct {
	URL     string
	Stream  string // e.g. WAL_7
	Subject string // e.g. wal.7
}

// JetStream is a Log backed by one NATS JetStream stream per shard. Stream
// sequence numbers double as offsets.
type JetStream struct {
	nc      *nats.Conn
	js      nats.JetStreamContext
	stream  string
	subject string

	tmu    sync.Mutex
	purged uint64
}

// OpenJetStream connects, creating the stream on first use.
func OpenJetStream(cfg JetStreamConfig) (*JetStream, error) {
	if cfg.URL == "" {
		cfg.URL = nats.DefaultURL
	}
	if cfg.Stream == "" || cfg.Subject == "" {
		return nil, errors.New("jetstream wal: stream and subject required")
	}
	nc, err := nats.Connect(cfg.URL, nats.Name("hapeserver-wal"))
	if err != nil {
		return nil, fmt.Errorf("nats connect: %w", err)
	}
	js, err := nc.JetStream()
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("jetstream context: %w", err)
	}
	w := &JetStream{nc: nc, js: js, stream: cfg.Stream, subject: cfg.Subject}
	if _, err := js.StreamInfo(cfg.Stream); err != nil {
		if _, addErr := js.AddStream(&nats.StreamConfig{
			Name:     cfg.Stream,
			Subjects: []string{cfg.Subject},
			Storage:  nats.FileStorage,
		}); addErr != nil {
			// A concurrent creator wins; re-check before failing.
			if _, infoErr := js.StreamInfo(cfg.Stream); infoErr != nil {
				nc.Close()
				return nil, fmt.Errorf("add stream: %w", addErr)
			}
		}
	}
	return w, nil
}

// Append publishes one record and returns its stream sequence.
func (w *JetStream) Append(record []byte) (uint64, error) {
	ack, err := w.js.Publish(w.subject, record)
	if err != nil {
		return 0, fmt.Errorf("wal publish: %w", err)
	}
	return ack.Sequence, nil
}

// Replay reads retained records in stream order.
func (w *JetStream) Replay(fn func(offset uint64, record []byte) error) error {
	sub, err := w.js.SubscribeSync(w.subject, nats.OrderedConsumer())
	if err != nil {
		return fmt.Errorf("wal subscribe: %w", err)
	}
	defer func() { _ = sub.Unsubscribe() }()

	for {
		msg, err := sub.NextMsg(200 * time.Millisecond)
		if errors.Is(err, nats.ErrTimeout) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("wal next: %w", err)
		}
		meta, err := msg.Metadata()
		if err != nil {
			return fmt.Errorf("wal metadata: %w", err)
		}
		if err := fn(meta.Sequence.Stream, msg.Data); err != nil {
			return err
		}
	}
}

// Truncate deletes records with offset < before. POC implementation deletes
// messages one by one; replace with a stream purge when the WAL rotation
// policy lands.
func (w *JetStream) Truncate(before uint64) error {
	w.tmu.Lock()
	defer w.tmu.Unlock()
	start := uint64(1)
	if w.purged > 0 {
		start = w.purged + 1
	}
	for seq := start; seq < before; seq++ {
		if err := w.js.DeleteMsg(w.stream, seq); err != nil {
			// Missing/already-deleted messages are fine.
			continue
		}
	}
	if before > 0 {
		w.purged = before - 1
	}
	return nil
}

// LastOffset returns the stream's last sequence.
func (w *JetStream) LastOffset() uint64 {
	info, err := w.js.StreamInfo(w.stream)
	if err != nil {
		return 0
	}
	return info.State.LastSeq
}

// Close releases the NATS connection.
func (w *JetStream) Close() error {
	w.nc.Close()
	return nil
}
