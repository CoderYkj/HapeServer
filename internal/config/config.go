// Package config loads the P0 process configuration.
package config

import (
	"encoding/json"
	"os"
)

// Config is the root document in configs/default.json.
type Config struct {
	Runtime Runtime `json:"runtime"`
	World   World   `json:"world"`
	Storage Storage `json:"storage"`
	Trade   Trade   `json:"trade"`
	Reset   Reset   `json:"reset"`
}

type Runtime struct {
	Kind string `json:"kind"` // goakt | inmem
}

type World struct {
	ID     uint32 `json:"id"`
	Region string `json:"region"`
	Shards int    `json:"shards"`
	Season uint32 `json:"season"`
}

type Storage struct {
	SnapshotDir string `json:"snapshotDir"`
	WALDir      string `json:"walDir"`
	WALKind     string `json:"walKind"` // memory | jetstream
	NATSURL     string `json:"natsUrl"`
	WALStream   string `json:"walStream"`
	WALSubject  string `json:"walSubject"`
	MongoURL    string `json:"mongoUrl"` // empty = in-memory trade store
	MongoDB     string `json:"mongoDb"`
}

type Trade struct {
	TimeoutSeconds int `json:"timeoutSeconds"`
	MaxDaily       int `json:"maxDaily"`
}

type Reset struct {
	SweepHours int `json:"sweepHours"`
}

// Defaults mirrors appendix D of the architecture document.
func Defaults() Config {
	return Config{
		Runtime: Runtime{Kind: "goakt"},
		World:   World{ID: 1, Region: "dev", Shards: 12, Season: 1},
		Storage: Storage{
			SnapshotDir: "./data/snapshots",
			WALDir:      "./data/wal",
			WALKind:     "memory",
			NATSURL:     "nats://127.0.0.1:4222",
			WALStream:   "WAL_1",
			WALSubject:  "wal.1",
			MongoDB:     "hapeserver",
		},
		Trade: Trade{TimeoutSeconds: 600, MaxDaily: 20},
		Reset: Reset{SweepHours: 24},
	}
}

// Load reads path, falling back to defaults for empty fields.
func Load(path string) (Config, error) {
	cfg := Defaults()
	b, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}
