package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/openmtx/meta-search-tool/internal/cli"
	"github.com/openmtx/meta-search-tool/internal/config"
	"github.com/openmtx/meta-search-tool/internal/engine"
	"github.com/openmtx/meta-search-tool/internal/format"
	"github.com/openmtx/meta-search-tool/internal/rank"
)

// perEngineCeiling is the 6s ceiling on the default per-engine timeout (§4):
// --per-engine-timeout defaults to min(--timeout, 6s), sized to fit the full
// 2-retry budget (§11).
const perEngineCeiling = 6 * time.Second

// plan is the fully-resolved description of what to run, produced by resolvePlan
// from flags + config + registry. It is the single input to dispatch + rank +
// format, and carries everything the §4/§6 precedence rules resolve.
type plan struct {
	engines    []engine.Engine          // ready engines to dispatch, in registration order
	weights    map[string]float64       // effective weight per engine name
	timeouts   map[string]time.Duration // effective per-engine timeout
	regOrder   map[string]int           // engine name -> registration index (§9 tie-break)
	fetchLimit int                      // per-engine fetch cap (§9)
	globalTo   time.Duration            // whole-run budget (§7)
	limit      int
	offset     int
	rrfK       int
}

// usageErr is a usage/config error mapping to exit code 2 (§4).
type usageErr struct{ msg string }

func (e *usageErr) Error() string { return e.msg }

func usageErrorf(format string, a ...any) error {
	return &usageErr{msg: fmt.Sprintf(format, a...)}
}

// resolvePlan computes the dispatch plan from flags + config + registry, applying
// the §4 engine-selection / §6 precedence rules and validating engine selection.
// Every returned error is a usage/config error → exit 2.
func resolvePlan(flags *cli.Flags, cfg *config.Config, reg *engine.Registry) (*plan, error) {
	// Expand category/group aliases in --engine list (§17.3, Sprint B.3.1).
	// This must happen before the unknown-name check below so category names
	// (e.g. "academic") are resolved to their constituent engines before
	// validation.
	flags.Engines = reg.ExpandNames(flags.Engines)

	names := reg.Names()
	nameSet := make(map[string]bool, len(names))
	for _, n := range names {
		nameSet[n] = true
	}
	valid := func() string { return strings.Join(names, ", ") }

	// Unknown engine names in any engine-name flag → exit 2 (decision 2026-07-07b:
	// fail-fast on typos, consistent with the documented --weight behavior).
	for _, list := range [][]string{flags.Engines, flags.Exclude, flags.Require} {
		for _, n := range list {
			if !nameSet[n] {
				return nil, usageErrorf("unknown engine %q; valid: %s", n, valid())
			}
		}
	}
	for n := range flags.Weight {
		if !nameSet[n] {
			return nil, usageErrorf("unknown engine %q in --weight; valid: %s", n, valid())
		}
	}

	// --require ∩ --exclude is a config conflict → exit 2 (§4).
	excludeSet := setOf(flags.Exclude)
	for _, r := range flags.Require {
		if excludeSet[r] {
			return nil, usageErrorf("engine %q is in both --require and --exclude", r)
		}
	}

	// Global timeout.
	globalTo, err := parseTimeout(flags.Timeout, config.DefaultTimeoutStr)
	if err != nil {
		return nil, usageErrorf("invalid --timeout %q: %v", flags.Timeout, err)
	}

	// Per-engine flag timeout (optional; defaults to min(global, 6s)).
	var flagPerEng time.Duration
	if flags.PerEngineTimeout != "" {
		flagPerEng, err = parseTimeout(flags.PerEngineTimeout, "")
		if err != nil {
			return nil, usageErrorf("invalid --per-engine-timeout %q: %v", flags.PerEngineTimeout, err)
		}
	}
	defaultPerEng := globalTo
	if perEngineCeiling < defaultPerEng {
		defaultPerEng = perEngineCeiling
	}

	// Compute each engine's operational state (§4 selection precedence, §6
	// weight/precedence) via the shared helper used by --list-engines too.
	states := computeStates(flags, cfg, reg)

	// --require: each must be Ready — a required engine that cannot start
	// (disabled / missing key / not whitelisted) → exit 2 (§4/§17.9).
	statusOf := func(name string) engine.Status {
		for _, s := range states {
			if s.info.Name == name {
				return s.status
			}
		}
		return engine.StatusDisabled
	}
	for _, r := range flags.Require {
		if st := statusOf(r); st != engine.StatusReady {
			return nil, usageErrorf("--require engine %q cannot start (%s)", r, st)
		}
	}

	// Build the plan from the Ready engines.
	p := &plan{
		weights:    make(map[string]float64),
		timeouts:   make(map[string]time.Duration),
		regOrder:   make(map[string]int),
		limit:      flags.Limit,
		offset:     flags.Offset,
		rrfK:       flags.RRFK,
		globalTo:   globalTo,
		fetchLimit: rank.FetchLimit(flags.Offset, flags.Limit),
	}
	for i, s := range states {
		name := s.info.Name
		p.regOrder[name] = i
		if s.status != engine.StatusReady {
			continue // auto-skip disabled / missing-key (§4)
		}
		p.weights[name] = s.weight // already resolved default → config → flag

		// Timeout: config per-engine → --per-engine-timeout → min(global, 6s).
		to := defaultPerEng
		if flagPerEng > 0 {
			to = flagPerEng
		}
		if ec := cfg.Engines[name]; ec != nil && ec.Timeout != "" {
			if parsed, perr := time.ParseDuration(ec.Timeout); perr == nil && parsed > 0 {
				to = parsed
			}
		}
		p.timeouts[name] = to

		p.engines = append(p.engines, reg.Get(name))
	}
	return p, nil
}

// parseTimeout parses a duration string, falling back to `fallback` when s is
// empty. A non-positive result is an error (timeouts must be positive).
func parseTimeout(s, fallback string) (time.Duration, error) {
	if s == "" {
		s = fallback
	}
	if s == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, err
	}
	if d <= 0 {
		return 0, fmt.Errorf("timeout must be positive")
	}
	return d, nil
}

func setOf(items []string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, x := range items {
		m[x] = true
	}
	return m
}

// engineState is the fully-resolved operational state of one registered engine,
// shared by resolvePlan (which builds the dispatch plan from the Ready states)
// and --list-engines (which renders the listing). It is the pure "what would
// each engine be?" view — NO validation (unknown-name / require∩exclude /
// require-cannot-start); those run-time guards live in resolvePlan. enabled and
// weight apply the §6 precedence rules (default → config → flag). weight is
// resolved for EVERY engine (not just Ready ones) so --list-engines can show
// effective weights even for disabled / missing-key engines.
type engineState struct {
	info     engine.EngineInfo
	enabled  bool
	keyed    bool
	keySet   bool // env var holding the key is set (only meaningful when keyed)
	weight   float64
	status   engine.Status
	regOrder int
}

// computeStates resolves every registered engine's state from flags + config +
// registry (§4 selection precedence, §6 weight/precedence), in registration
// order. It performs no validation — it is the shared input to both the dispatch
// plan and the --list-engines listing.
func computeStates(flags *cli.Flags, cfg *config.Config, reg *engine.Registry) []engineState {
	names := reg.Names()
	whitelist := setOf(flags.Engines)
	excludeSet := setOf(flags.Exclude)

	states := make([]engineState, 0, len(names))
	for i, n := range names {
		info := reg.Get(n).Descriptor()
		ec := cfg.Engines[n]

		// enabled: default → config → --engine whitelist → --exclude.
		enabled := info.Enabled
		if ec != nil && ec.Enabled != nil {
			enabled = *ec.Enabled
		}
		if len(whitelist) > 0 {
			enabled = whitelist[n]
		}
		if excludeSet[n] {
			enabled = false
		}

		keyed := info.Keyed
		keySet := info.KeyEnv != "" && os.Getenv(info.KeyEnv) != ""

		var status engine.Status
		switch {
		case !enabled:
			status = engine.StatusDisabled
		case keyed && !keySet:
			status = engine.StatusMissingKey
		default:
			status = engine.StatusReady
		}

		// weight: default → config(>0) → --weight flag.
		w := info.DefaultWeight
		if ec != nil && ec.Weight > 0 {
			w = ec.Weight
		}
		if fw, ok := flags.Weight[n]; ok {
			w = fw
		}

		states = append(states, engineState{
			info: info, enabled: enabled, keyed: keyed, keySet: keySet,
			weight: w, status: status, regOrder: i,
		})
	}
	return states
}

// computeListEntries builds the --list-engines listing (§4): one entry per
// registered engine with name/enabled/keyed/key_present/weight/status.
// key_present is nil (→ JSON null) for keyless engines, a real bool otherwise.
// Precedence is identical to a normal run (§6) because it reuses computeStates,
// so --weight/--engine/--exclude + config + env are all reflected.
func computeListEntries(flags *cli.Flags, cfg *config.Config, reg *engine.Registry) []format.ListEngine {
	// Expand category/group aliases so --list-engines with --engine academic
	// shows only academic engines (arxiv, pubmed).
	flags.Engines = reg.ExpandNames(flags.Engines)

	states := computeStates(flags, cfg, reg)
	out := make([]format.ListEngine, 0, len(states))
	for _, s := range states {
		var keyPresent *bool
		if s.keyed {
			kp := s.keySet
			keyPresent = &kp
		}
		out = append(out, format.ListEngine{
			Name:       s.info.Name,
			Category:   s.info.Category,
			Enabled:    s.enabled,
			Keyed:      s.keyed,
			KeyPresent: keyPresent,
			Weight:     s.weight,
			Status:     s.status.String(),
		})
	}
	return out
}
