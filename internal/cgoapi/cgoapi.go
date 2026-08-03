// Package cgoapi is the shared application core of mst: the engine-plan
// resolution and run orchestration consumed by BOTH the CLI (cmd/mst) and the
// CGO bridge (cgo/). Centralizing it here means the dispatch plan, per-engine
// state resolution, --dry-run rendering, --list-engines listing, exit-code
// computation, tavily-answer extraction, and the default engine registry each
// live in exactly one place — the CLI and the Python binding share one
// implementation instead of carrying parallel copies.
package cgoapi

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/openmtx/meta-search-tool/internal/cli"
	"github.com/openmtx/meta-search-tool/internal/config"
	"github.com/openmtx/meta-search-tool/internal/engine"
	"github.com/openmtx/meta-search-tool/internal/engine/arxiv"
	"github.com/openmtx/meta-search-tool/internal/engine/bing"
	"github.com/openmtx/meta-search-tool/internal/engine/brave"
	"github.com/openmtx/meta-search-tool/internal/engine/ddg"
	"github.com/openmtx/meta-search-tool/internal/engine/google"
	"github.com/openmtx/meta-search-tool/internal/engine/grokipedia"
	"github.com/openmtx/meta-search-tool/internal/engine/hackernews"
	"github.com/openmtx/meta-search-tool/internal/engine/mojeek"
	"github.com/openmtx/meta-search-tool/internal/engine/pubmed"
	"github.com/openmtx/meta-search-tool/internal/engine/stackexchange"
	"github.com/openmtx/meta-search-tool/internal/engine/tavily"
	"github.com/openmtx/meta-search-tool/internal/engine/wikipedia"
	"github.com/openmtx/meta-search-tool/internal/format"
	"github.com/openmtx/meta-search-tool/internal/rank"
	"github.com/openmtx/meta-search-tool/internal/search"
)

// PerEngineCeiling is the 6s ceiling on the default per-engine timeout (§4):
// --per-engine-timeout defaults to min(--timeout, 6s), sized to fit the full
// 2-retry budget (§11).
const PerEngineCeiling = 6 * time.Second

// Plan is the fully-resolved description of what to run, produced by
// ResolvePlan from flags + config + registry. It is the single input to
// dispatch + rank + format, and carries everything the §4/§6 precedence rules
// resolve.
type Plan struct {
	Engines    []engine.Engine          // ready engines to dispatch, in registration order
	Weights    map[string]float64       // effective weight per engine name
	Timeouts   map[string]time.Duration // effective per-engine timeout
	RegOrder   map[string]int           // engine name -> registration index (§9 tie-break)
	FetchLimit int                      // per-engine fetch cap (§9)
	GlobalTo   time.Duration            // whole-run budget (§7)
	Limit      int
	Offset     int
	RRFK       int
	EngineOpts map[string]engine.Options // per-engine extra options (e.g. tavily include_answer, search_depth)
}

// usageErr is a usage/config error mapping to exit code 2 (§4).
type usageErr struct{ msg string }

func (e *usageErr) Error() string { return e.msg }

func usageErrorf(format string, a ...any) error {
	return &usageErr{msg: fmt.Sprintf(format, a...)}
}

// resolved holds the shared output of resolve(), consumed by both ResolvePlan
// (build a dispatch plan from the Ready engines) and ResolveDryRun (render the
// plan for inspection) so the validation + state prelude exists exactly once.
type resolved struct {
	states        []engineState
	globalTo      time.Duration
	defaultPerEng time.Duration
	flagPerEng    time.Duration
	engineOpts    map[string]engine.Options
}

// resolve performs the validation + state resolution shared by ResolvePlan and
// ResolveDryRun. It applies the §4 engine-selection and §6 precedence rules and
// validates engine selection; every returned error is a usage/config error
// (exit 2). It performs no dispatch and renders no output.
func resolve(flags *cli.Flags, cfg *config.Config, reg *engine.Registry) (*resolved, error) {
	// Expand category/group aliases in --engine (§17.3). This must happen before
	// the unknown-name check below so category names (e.g. "academic") resolve to
	// their constituent engines before validation.
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
	excludeSet := SetOf(flags.Exclude)
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
	if PerEngineCeiling < defaultPerEng {
		defaultPerEng = PerEngineCeiling
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

	// Build per-engine extra options from config (e.g. tavily include_answer,
	// search_depth).
	engineOpts := make(map[string]engine.Options)
	for name, ec := range cfg.Engines {
		var opts engine.Options
		if ec.IncludeAnswer != nil {
			opts.IncludeAnswer = *ec.IncludeAnswer
		}
		opts.SearchDepth = ec.SearchDepth
		engineOpts[name] = opts
	}

	return &resolved{
		states:        states,
		globalTo:      globalTo,
		defaultPerEng: defaultPerEng,
		flagPerEng:    flagPerEng,
		engineOpts:    engineOpts,
	}, nil
}

// effectiveTimeout resolves one Ready engine's timeout (§4/§6 precedence):
// config per-engine → --per-engine-timeout → min(--timeout, 6s).
func effectiveTimeout(name string, defaultPerEng, flagPerEng time.Duration, cfg *config.Config) time.Duration {
	to := defaultPerEng
	if flagPerEng > 0 {
		to = flagPerEng
	}
	if ec := cfg.Engines[name]; ec != nil && ec.Timeout != "" {
		if parsed, err := time.ParseDuration(ec.Timeout); err == nil && parsed > 0 {
			to = parsed
		}
	}
	return to
}

// ResolvePlan computes the dispatch plan from flags + config + registry,
// applying the §4 engine-selection / §6 precedence rules and validating engine
// selection. Every returned error is a usage/config error → exit 2.
func ResolvePlan(flags *cli.Flags, cfg *config.Config, reg *engine.Registry) (*Plan, error) {
	r, err := resolve(flags, cfg, reg)
	if err != nil {
		return nil, err
	}

	p := &Plan{
		Weights:    make(map[string]float64),
		Timeouts:   make(map[string]time.Duration),
		RegOrder:   make(map[string]int),
		Limit:      flags.Limit,
		Offset:     flags.Offset,
		RRFK:       flags.RRFK,
		GlobalTo:   r.globalTo,
		FetchLimit: rank.FetchLimit(flags.Offset, flags.Limit),
		EngineOpts: r.engineOpts,
	}
	for i, s := range r.states {
		name := s.info.Name
		p.RegOrder[name] = i
		if s.status != engine.StatusReady {
			continue // auto-skip disabled / missing-key (§4)
		}
		p.Weights[name] = s.weight // already resolved default → config → flag
		p.Timeouts[name] = effectiveTimeout(name, r.defaultPerEng, r.flagPerEng, cfg)
		p.Engines = append(p.Engines, reg.Get(name))
	}
	return p, nil
}

// DryRunOutput is the JSON structure emitted by --dry-run. It shows the
// resolved plan: which engines will run, their timeouts, weights, and the
// global timeout / fetch limit. Intended for tool wrappers that want to inspect
// the plan before dispatching.
type DryRunOutput struct {
	Query      string         `json:"query"`
	Engines    []DryRunEngine `json:"engines"`
	GlobalTo   string         `json:"global_timeout"`
	FetchLimit int            `json:"fetch_limit"`
	Limit      int            `json:"limit"`
	Offset     int            `json:"offset"`
	RRFK       int            `json:"rrf_k"`
}

// DryRunEngine describes one engine's resolved state in the plan.
type DryRunEngine struct {
	Name     string  `json:"name"`
	Status   string  `json:"status"`
	Weight   float64 `json:"weight"`
	Timeout  string  `json:"timeout,omitempty"` // only for ready engines
	Category string  `json:"category"`
}

// ResolveDryRun resolves the full plan (same validation path as ResolvePlan)
// and returns a serializable dry-run representation: every registered engine's
// status, weight, category, and (for Ready engines) effective timeout, plus the
// global run parameters. Intended for --dry-run inspection by tool wrappers.
func ResolveDryRun(flags *cli.Flags, cfg *config.Config, reg *engine.Registry) (*DryRunOutput, error) {
	r, err := resolve(flags, cfg, reg)
	if err != nil {
		return nil, err
	}

	out := &DryRunOutput{
		Query:      flags.Query,
		GlobalTo:   r.globalTo.String(),
		Limit:      flags.Limit,
		Offset:     flags.Offset,
		RRFK:       flags.RRFK,
		FetchLimit: rank.FetchLimit(flags.Offset, flags.Limit),
	}
	for _, s := range r.states {
		dre := DryRunEngine{
			Name:     s.info.Name,
			Status:   s.status.String(),
			Weight:   s.weight,
			Category: s.info.Category,
		}
		if s.status == engine.StatusReady {
			dre.Timeout = effectiveTimeout(s.info.Name, r.defaultPerEng, r.flagPerEng, cfg).String()
		}
		out.Engines = append(out.Engines, dre)
	}
	return out, nil
}

// engineState is the fully-resolved operational state of one registered engine,
// shared by ResolvePlan (which builds the dispatch plan from the Ready states)
// and ComputeListEntries (which renders the listing). It is the pure "what
// would each engine be?" view — NO validation (unknown-name / require∩exclude /
// require-cannot-start); those run-time guards live in resolve(). enabled and
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
	whitelist := SetOf(flags.Engines)
	excludeSet := SetOf(flags.Exclude)

	// When no --engine whitelist is specified, default to "general" category
	// only (§4: --engine defaults to *general*; v1.0 scope is general web
	// search, so the default selects only General-category engines). An explicit
	// --engine (e.g. --engine academic, --engine all) expands to the requested
	// set, overriding this default.
	useGeneralOnly := len(whitelist) == 0

	states := make([]engineState, 0, len(names))
	for i, n := range names {
		info := reg.Get(n).Descriptor()
		ec := cfg.Engines[n]

		// enabled: default → config → --engine whitelist → category default → --exclude.
		enabled := info.Enabled
		if ec != nil && ec.Enabled != nil {
			enabled = *ec.Enabled
		}
		if useGeneralOnly {
			enabled = enabled && info.Category == "general"
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

// ComputeListEntries builds the --list-engines listing (§4): one entry per
// registered engine with name/enabled/keyed/key_present/weight/status.
// key_present is nil (→ JSON null) for keyless engines, a real bool otherwise.
// Precedence is identical to a normal run (§6) because it reuses computeStates,
// so --weight/--engine/--exclude + config + env are all reflected.
func ComputeListEntries(flags *cli.Flags, cfg *config.Config, reg *engine.Registry) []format.ListEngine {
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

// SetOf returns a set view of items for membership lookup.
func SetOf(items []string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, x := range items {
		m[x] = true
	}
	return m
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

// DefaultRegistry builds the engine registry and registers all available
// engines. Engines that require keys but don't have them set are auto-skipped at
// runtime by ResolvePlan (StatusMissingKey), so they appear in --list-engines as
// "missing-key" rather than silently vanishing.
func DefaultRegistry() *engine.Registry {
	reg := engine.NewRegistry()
	if err := reg.Register(arxiv.New()); err != nil {
		panic("arxiv: " + err.Error())
	}
	if err := reg.Register(pubmed.New()); err != nil {
		panic("pubmed: " + err.Error())
	}
	if err := reg.Register(stackexchange.New()); err != nil {
		panic("stackexchange: " + err.Error())
	}
	if err := reg.Register(hackernews.New()); err != nil {
		panic("hackernews: " + err.Error())
	}
	if err := reg.Register(grokipedia.New()); err != nil {
		panic("grokipedia: " + err.Error())
	}
	if err := reg.Register(brave.New()); err != nil {
		panic("brave: " + err.Error())
	}
	if err := reg.Register(mojeek.New()); err != nil {
		panic("mojeek: " + err.Error())
	}
	if err := reg.Register(wikipedia.New()); err != nil {
		panic("wikipedia: " + err.Error())
	}
	if err := reg.Register(ddg.New()); err != nil {
		panic("ddg: " + err.Error())
	}
	if err := reg.Register(google.New()); err != nil {
		panic("google: " + err.Error())
	}
	if err := reg.Register(bing.New()); err != nil {
		panic("bing: " + err.Error())
	}
	if err := reg.Register(tavily.New()); err != nil {
		panic("tavily: " + err.Error())
	}
	return reg
}

// ExtractTavilyAnswer extracts the AI-generated answer from the tavily engine's
// raw payload, if present. Returns empty string if tavily didn't run, didn't
// return an answer, or include_answer was not enabled. The answer is surfaced
// as a separate top-level field in the output envelope (§5 Tier D, Sprint
// D.1.2) and does NOT compose with RRF.
func ExtractTavilyAnswer(runs []engine.EngineRun) string {
	for _, r := range runs {
		if r.Engine == "tavily" && r.Raw != nil {
			return tavily.GetAnswerFromRaw(r.Raw)
		}
	}
	return ""
}

// ComputeExit returns the §4 exit code from the run outcome. Precedence
// (highest first): 2 > 3 > 1 > 0. Exit 2 is decided upstream (before dispatch);
// this only distinguishes 0 / 1 / 3. Exit 3 = a required engine ran but failed,
// OR --strict with any engine failure; exit 3 beats exit 1 when both apply.
// resultCount is the number of output items produced (merged results, unmerged
// results, or raw payloads) — 0 ⇒ exit 1.
func ComputeExit(resultCount int, runs []engine.EngineRun, requireSet map[string]bool, strict bool) int {
	for _, r := range runs {
		if r.Err != nil && (requireSet[r.Engine] || strict) {
			return 3
		}
	}
	if resultCount == 0 {
		return 1
	}
	return 0
}

// --- CGO-facing API (consumed by cgo/main.go → Python binding) ---------------

// MergedRun is the outcome of the merged execute path (dispatch → rank → window
// → envelope → exit). It exposes the dispatch Runs (for the CLI's stderr error
// reporting) and the ranked Results (for the CLI-only --fields projection)
// alongside the output Envelope and Exit code, so the CLI and the CGO bridge
// share one execute function while still doing their own rendering.
type MergedRun struct {
	Envelope *format.Envelope
	Runs     []engine.EngineRun
	Results  []rank.RankedResult
	Exit     int
}

// RunMerged is the shared merged-path execute function: dispatch the plan's
// engines, rank+window the results (§9), extract the tavily answer (§5 Tier D),
// build the output envelope (§10), and compute the §4 exit code. It performs no
// I/O. requireSet/strict feed the exit-3 rule. The CLI (cmd/mst) and the CGO
// bridge (cgoapi.Search) both call this so the merged mechanics cannot drift
// between the two surfaces.
func RunMerged(ctx context.Context, p *Plan, query string, requireSet map[string]bool, strict bool) *MergedRun {
	start := time.Now()
	runs := search.Dispatch(ctx, p.Engines, query, p.FetchLimit, p.Timeouts, p.EngineOpts)
	results := rank.Window(rank.Rank(runs, p.Weights, p.RegOrder, p.RRFK), p.Offset, p.Limit)
	tookMs := time.Since(start).Milliseconds()

	answer := ExtractTavilyAnswer(runs)
	env := format.BuildEnvelope(query, runs, results, p.Limit, tookMs, answer)
	exit := ComputeExit(len(results), runs, requireSet, strict)
	return &MergedRun{Envelope: env, Runs: runs, Results: results, Exit: exit}
}

// SearchOptions carries Python-side overrides for a search run.
type SearchOptions struct {
	Engines []string
	Limit   int
	Offset  int
	RRFK    int
	Timeout time.Duration
	Weights map[string]float64
}

// SearchResult is the parsed output from a search call.
type SearchResult struct {
	Envelope *format.Envelope
	ExitCode int
}

// Search executes a meta-search query with the given options and returns the
// result envelope plus exit code. It is the merged-path entrypoint for the CGO
// bridge.
//
// NOTE: this builds a partial cli.Flags from SearchOptions and therefore exposes
// only a subset of the CLI surface (no --exclude/--require/--strict/--raw/
// --no-merge/--fields/--per-engine-timeout). See DECISIONS 2026-07-11.
func Search(query string, opts SearchOptions) (*SearchResult, error) {
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("empty query")
	}

	reg := DefaultRegistry()
	cfg, _ := config.Load()
	cfg.Defaults.Limit = opts.Limit
	cfg.Defaults.Timeout = opts.Timeout.String()

	for name, w := range opts.Weights {
		if cfg.Engines[name] == nil {
			cfg.Engines[name] = &config.EngineConfig{}
		}
		cfg.Engines[name].Weight = w
	}

	flags := &cli.Flags{
		Query:   query,
		Limit:   opts.Limit,
		Offset:  opts.Offset,
		RRFK:    opts.RRFK,
		Timeout: opts.Timeout.String(),
		Format:  "json",
		Engines: opts.Engines,
		Weight:  opts.Weights,
	}

	p, err := ResolvePlan(flags, cfg, reg)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), opts.Timeout)
	defer cancel()

	mr := RunMerged(ctx, p, query, nil, false)
	return &SearchResult{Envelope: mr.Envelope, ExitCode: mr.Exit}, nil
}

// ListEnginesResult holds the output of a list-engines call.
type ListEnginesResult struct {
	Entries []format.ListEngine
}

// LoadConfig loads the user configuration file (same as config.Load).
func LoadConfig() (*config.Config, error) {
	return config.Load()
}

// ListEngines resolves and returns all registered engines with their state.
func ListEngines() (*ListEnginesResult, error) {
	reg := DefaultRegistry()
	cfg, _ := LoadConfig()

	flags := &cli.Flags{
		Limit:   10,
		Format:  "json",
		Timeout: "8s",
	}

	entries := ComputeListEntries(flags, cfg, reg)
	return &ListEnginesResult{Entries: entries}, nil
}
