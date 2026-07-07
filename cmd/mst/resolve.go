package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/openmtx/meta-search-tool/internal/cli"
	"github.com/openmtx/meta-search-tool/internal/config"
	"github.com/openmtx/meta-search-tool/internal/engine"
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

	// Compute each engine's operational state (§4 selection precedence).
	whitelist := setOf(flags.Engines)
	type estate struct {
		eng    engine.Engine
		info   engine.EngineInfo
		status engine.Status
	}
	states := make([]estate, 0, len(names))
	for _, n := range names {
		e := reg.Get(n)
		info := e.Descriptor()
		ec := cfg.Engines[n]

		// enabled: config *bool → else default; --engine whitelist overrides;
		// --exclude always removes.
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

		keyPresent := !info.Keyed || os.Getenv(info.KeyEnv) != ""
		var status engine.Status
		switch {
		case !enabled:
			status = engine.StatusDisabled
		case info.Keyed && !keyPresent:
			status = engine.StatusMissingKey
		default:
			status = engine.StatusReady
		}
		states = append(states, estate{eng: e, info: info, status: status})
	}

	// --require: each must be Ready — a required engine that cannot start
	// (disabled / missing key / not whitelisted) → exit 2 (§4/§17.9).
	statusOf := func(name string) engine.Status {
		for _, s := range states {
			if s.eng.Name() == name {
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
		p.regOrder[s.eng.Name()] = i
		if s.status != engine.StatusReady {
			continue // auto-skip disabled / missing-key (§4)
		}
		name := s.eng.Name()

		// Weight: default → config (>0) → --weight flag.
		w := s.info.DefaultWeight
		if ec := cfg.Engines[name]; ec != nil && ec.Weight > 0 {
			w = ec.Weight
		}
		if fw, ok := flags.Weight[name]; ok {
			w = fw
		}
		p.weights[name] = w

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

		p.engines = append(p.engines, s.eng)
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
