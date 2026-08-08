// Package writer implements scribe.Writer connectors: the secondary agents
// that actually produce doc edits. Decision 6 in docs/PLAN.md: the writer is
// config, not a hardcoded dependency, so this package is a small registry of
// named connectors plus a "custom" escape hatch, rather than one generic
// command template.
package writer

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Sahil-796/scribe/internal/scribe"
)

// Config configures a writer connector. Phase 01 hardcodes everything else
// (decision: "one repo, one person, nothing configurable yet") — this struct
// is deliberately the only knob, and most of its fields are optional.
type Config struct {
	// Agent selects the connector: "opencode" or "custom". Required.
	Agent string

	// Model is passed through to connectors that take a model flag (e.g.
	// opencode's "opencode/deepseek-v4-flash-free"). Ignored by connectors
	// that don't use it.
	Model string

	// Command overrides the binary to run. For "opencode" this is a path
	// override (defaults to "opencode" on $PATH). For "custom" it's a
	// whitespace-split command line, used only if Args is empty — prefer
	// Args when any part of the command might contain a space.
	Command string

	// Args, for "custom" only, is the full argv (Args[0] is the binary).
	// Takes precedence over Command.
	Args []string

	// Timeout is the hard ceiling on one Run call. Zero means defaultTimeout.
	Timeout time.Duration
}

// registry maps agent name to constructor. A registry rather than a switch
// so claude, codex and gemini can be added later by calling Register (or by
// adding an entry in this package) without touching any caller of New.
var registry = map[string]func(Config) (scribe.Writer, error){
	"opencode": newOpencodeWriter,
	"custom":   newCustomWriter,
}

// Register adds or replaces the constructor for an agent name. Exported so a
// connector can live outside this package if that's ever useful, though in
// practice new connectors are expected to just add themselves to registry
// above the way opencode and custom do.
func Register(agent string, factory func(Config) (scribe.Writer, error)) {
	registry[agent] = factory
}

// New builds the connector named by cfg.Agent.
func New(cfg Config) (scribe.Writer, error) {
	if cfg.Agent == "" {
		return nil, fmt.Errorf("writer: Config.Agent is required (one of: %s)", strings.Join(registeredNames(), ", "))
	}
	factory, ok := registry[cfg.Agent]
	if !ok {
		return nil, fmt.Errorf("writer: unknown agent %q (registered: %s)", cfg.Agent, strings.Join(registeredNames(), ", "))
	}
	return factory(cfg)
}

func registeredNames() []string {
	names := make([]string, 0, len(registry))
	for n := range registry {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
