// Package engine defines the Engine interface, EngineInfo metadata, and the
// registry that holds all registered engines.
package engine

import (
	"context"
	"fmt"
	"time"
)

// Engine is a pure data-fetching plugin. It knows nothing about dispatch,
// latency, or merge metadata — the dispatcher wraps its output (EngineRun).
type Engine interface {
	// Name returns the canonical engine name used in flags and output.
	Name() string
	// Descriptor returns static metadata about the engine.
	Descriptor() EngineInfo
	// Search executes a search query. The caller (dispatcher) wraps the
	// result into an EngineRun with timing / error metadata. ctx carries the
	// per-engine timeout/deadline (§7, §11) — engines must honor cancellation.
	Search(ctx context.Context, q string, opts Options) ([]Result, error)
}

// Result is the normalized, engine-agnostic unit returned by every engine.
type Result struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet,omitempty"`
	Source  string `json:"source"` // registered domain (eTLD+1)
}

// Options carries engine inputs. Limit is the per-engine FETCH cap derived
// in §9 (max(--limit*3, 20)) — NOT the user's --limit which bounds only the
// final merged output.
//
// IncludeAnswer and SearchDepth are tavily-specific options (§5 Tier D) that
// control whether the API returns an AI-generated answer and the search depth
// ("basic" or "advanced"). They are zero-valued for other engines.
type Options struct {
	Limit         int
	IncludeAnswer bool   // Tavily: request AI-generated answer in response
	SearchDepth   string // Tavily: "basic" (default) or "advanced"
}

// EngineInfo is static, compile-time metadata declared by each engine. The
// registry reads it to drive --list-engines and key/weight resolution.
type EngineInfo struct {
	Name          string
	Keyed         bool
	KeyEnv        string // env var name holding the key; empty if Keyed is false
	DefaultWeight float64
	Enabled       bool   // default enabled state (false for fragile/opt-in engines)
	Category      string // vertical specialty (§17.3): "general","academic","news","reference","q&a"
}

// EngineRun is assembled by the dispatcher (internal/search), NOT by engines.
// It carries the per-engine execution metadata surfaced in the output schema
// (§10): which engine ran, its results in engine-native rank order, the error
// (if it failed/timed out), and the wall-clock latency of the call. Raw holds
// the engine's parsed native payload and is populated only under --raw (§10);
// nil otherwise. The ranker (internal/rank, §9) consumes []EngineRun and
// produces its own aggregated result type (rank, score, contributing engines[]).
type EngineRun struct {
	Engine  string
	Results []Result // in engine-native rank order
	Err     error
	Latency time.Duration
	Raw     any // parsed native payload (set only under --raw, §10); nil otherwise
}

// RawEngine is an Engine that can also expose its parsed native payload — the
// pre-normalization structure with engine-native fields — for --raw (§10). The
// dispatcher calls SearchWithRaw instead of Search only when collecting raw
// payloads; engines that do not implement it yield no raw entry (their status
// is still reported in the `engines` block). The payload shape varies per
// engine and is intentionally unstructured (§10).
type RawEngine interface {
	Engine
	SearchWithRaw(ctx context.Context, q string, opts Options) ([]Result, any, error)
}

// Status describes the operational state of a keyed engine.
type Status int

const (
	StatusReady      Status = iota // engine works, key present
	StatusMissingKey               // engine requires a key that isn't set
	StatusDisabled                 // engine explicitly disabled in config/flags
)

func (s Status) String() string {
	switch s {
	case StatusReady:
		return "ready"
	case StatusMissingKey:
		return "missing-key"
	case StatusDisabled:
		return "disabled"
	default:
		return "unknown"
	}
}

// Registry holds all registered engines and resolves their effective state.
// Registration order is preserved so enumeration (Names, Infos) is deterministic
// — the ranker (§9) relies on registration order as a deterministic tie-breaker.
type Registry struct {
	engines map[string]Engine // keyed by engine name
	order   []string          // registration order
}

// NewRegistry creates an empty registry. Engines are added via Register().
func NewRegistry() *Registry {
	return &Registry{
		engines: make(map[string]Engine),
	}
}

// Register adds an engine to the registry. Returns an error if the name is
// already registered.
func (r *Registry) Register(e Engine) error {
	name := e.Name()
	if _, exists := r.engines[name]; exists {
		return fmt.Errorf("engine %q already registered", name)
	}
	r.engines[name] = e
	r.order = append(r.order, name)
	return nil
}

// Get returns the engine with the given name, or nil if not found.
func (r *Registry) Get(name string) Engine {
	return r.engines[name]
}

// All returns all registered engines keyed by name.
func (r *Registry) All() map[string]Engine {
	return r.engines
}

// Names returns the registered engine names in registration order (deterministic).
func (r *Registry) Names() []string {
	out := make([]string, len(r.order))
	copy(out, r.order)
	return out
}

// Infos returns the Descriptor() metadata for every registered engine in
// registration order — this is how the registry enumerates engines to drive
// --list-engines and key/weight resolution (§4, §8).
func (r *Registry) Infos() []EngineInfo {
	out := make([]EngineInfo, 0, len(r.order))
	for _, name := range r.order {
		out = append(out, r.engines[name].Descriptor())
	}
	return out
}

// ExpandNames expands category names and built-in group aliases in a name list
// (§4, §17.3). Category names are replaced by all engines registered with that
// category. Built-in aliases expand to all engines matching the property:
//
//	"free"  → all keyless engines
//	"keyed" → all keyed (requires key) engines
//	"all"   → all engines
//
// Names that are not category or alias names pass through unchanged (they will
// be validated as engine names upstream). Duplicates are removed (first
// occurrence wins, preserving order). Category and alias names are reserved
// and should not be used as engine names.
func (r *Registry) ExpandNames(names []string) []string {
	// Build category → engine names map from registered engines.
	catMap := make(map[string][]string)
	for _, info := range r.Infos() {
		if info.Category != "" {
			catMap[info.Category] = append(catMap[info.Category], info.Name)
		}
	}

	seen := make(map[string]bool)
	out := make([]string, 0, len(names))
	for _, n := range names {
		if seen[n] {
			continue
		}
		switch n {
		case "all":
			for _, info := range r.Infos() {
				if !seen[info.Name] {
					seen[info.Name] = true
					out = append(out, info.Name)
				}
			}
		case "free":
			for _, info := range r.Infos() {
				if !info.Keyed && !seen[info.Name] {
					seen[info.Name] = true
					out = append(out, info.Name)
				}
			}
		case "keyed":
			for _, info := range r.Infos() {
				if info.Keyed && !seen[info.Name] {
					seen[info.Name] = true
					out = append(out, info.Name)
				}
			}
		default:
			// Check if it's a category name.
			if expanded, ok := catMap[n]; ok {
				for _, en := range expanded {
					if !seen[en] {
						seen[en] = true
						out = append(out, en)
					}
				}
			} else {
				// Pass through unchanged (engine name; validated upstream).
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	return out
}
