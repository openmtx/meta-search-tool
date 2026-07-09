package main

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/openmtx/meta-search-tool/internal/cli"
	"github.com/openmtx/meta-search-tool/internal/config"
	"github.com/openmtx/meta-search-tool/internal/engine"
)

// dryRunOutput is the JSON structure emitted by --dry-run. It shows the
// resolved plan: which engines will run, their timeouts, weights, and the
// global timeout / fetch limit. Intended for tool wrappers that want to
// inspect the plan before dispatching.
type dryRunOutput struct {
	Query      string         `json:"query"`
	Engines    []dryRunEngine `json:"engines"`
	GlobalTo   string         `json:"global_timeout"`
	FetchLimit int            `json:"fetch_limit"`
	Limit      int            `json:"limit"`
	Offset     int            `json:"offset"`
	RRFK       int            `json:"rrf_k"`
}

// dryRunEngine describes one engine's resolved state in the plan.
type dryRunEngine struct {
	Name     string  `json:"name"`
	Status   string  `json:"status"`
	Weight   float64 `json:"weight"`
	Timeout  string  `json:"timeout,omitempty"` // only for ready engines
	Category string  `json:"category"`
}

// resolveDryRun resolves the full plan (same path as resolvePlan) and returns
// a serializable dry-run representation. It accepts flags, config, and registry
// so it can be called from run() with the same inputs.
func resolveDryRun(flags *cli.Flags, cfg *config.Config, reg *engine.Registry) (*dryRunOutput, error) {
	// Expand aliases (same as resolvePlan).
	flags.Engines = reg.ExpandNames(flags.Engines)

	names := reg.Names()
	nameSet := make(map[string]bool, len(names))
	for _, n := range names {
		nameSet[n] = true
	}
	valid := func() string { return stringsJoin(names, ", ") }

	// Validate engine names (same as resolvePlan).
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

	// --require ∩ --exclude.
	excludeSet := setOf(flags.Exclude)
	for _, r := range flags.Require {
		if excludeSet[r] {
			return nil, usageErrorf("engine %q is in both --require and --exclude", r)
		}
	}

	globalTo, err := parseTimeout(flags.Timeout, config.DefaultTimeoutStr)
	if err != nil {
		return nil, usageErrorf("invalid --timeout %q: %v", flags.Timeout, err)
	}

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

	states := computeStates(flags, cfg, reg)

	// --require: each must be Ready.
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

	out := &dryRunOutput{
		Query:      flags.Query,
		GlobalTo:   globalTo.String(),
		FetchLimit: 0, // will be set below
		Limit:      flags.Limit,
		Offset:     flags.Offset,
		RRFK:       flags.RRFK,
	}

	for _, s := range states {
		name := s.info.Name
		dre := dryRunEngine{
			Name:     name,
			Status:   s.status.String(),
			Weight:   s.weight,
			Category: s.info.Category,
		}

		if s.status == engine.StatusReady {
			to := defaultPerEng
			if flagPerEng > 0 {
				to = flagPerEng
			}
			if ec := cfg.Engines[name]; ec != nil && ec.Timeout != "" {
				if parsed, perr := time.ParseDuration(ec.Timeout); perr == nil && parsed > 0 {
					to = parsed
				}
			}
			dre.Timeout = to.String()
		}

		out.Engines = append(out.Engines, dre)
	}

	// fetchLimit is computed the same way as in resolvePlan.
	out.FetchLimit = rankFetchLimit(flags.Offset, flags.Limit)

	return out, nil
}

// emitDryRun writes the dry-run JSON to w and returns any marshal error.
func emitDryRun(w io.Writer, dry *dryRunOutput) error {
	data, err := json.MarshalIndent(dry, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal dry-run: %w", err)
	}
	data = append(data, '\n')
	_, err = w.Write(data)
	return err
}

// rankFetchLimit computes the per-engine fetch limit from offset and limit
// (§9): max((offset+limit)*3, 20).
func rankFetchLimit(offset, limit int) int {
	f := (offset + limit) * 3
	if f < 20 {
		f = 20
	}
	return f
}

// stringsJoin joins a string slice with ", ".
func stringsJoin(ss []string, sep string) string {
	if len(ss) == 0 {
		return ""
	}
	result := ss[0]
	for i := 1; i < len(ss); i++ {
		result += sep + ss[i]
	}
	return result
}
