// Package runtime selects a concrete ActorSystem implementation.
package runtime

import (
	"fmt"

	"hapeserver/internal/actor"
	"hapeserver/internal/config"
	goaktrt "hapeserver/internal/runtime/goakt"
	"hapeserver/internal/runtime/inmem"
)

// New returns the system configured for the current process.
func New(cfg config.Runtime) (actor.System, error) {
	switch cfg.Kind {
	case "", "goakt":
		return goaktrt.New()
	case "inmem":
		return inmem.New(), nil
	default:
		return nil, fmt.Errorf("unknown runtime kind %q", cfg.Kind)
	}
}
