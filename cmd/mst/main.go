// mst — Meta-Search Tool.
//
// A command-line meta-search tool that fans out concurrent queries to multiple
// search engines, merges and re-ranks the results (RRF), and emits them as JSON
// (or jsonl/text) for an AI agent harness to consume. See docs/SPEC.md.
//
// The version string is set at build time with -ldflags "-X main.version=X.Y.Z".
// Defaults to "dev" when not provided.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
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

// version is set at build time with -ldflags "-X main.version=v0.3.0".
// Defaults to "dev" for local builds.
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, defaultRegistry()))
}

// defaultRegistry builds the engine registry and registers all available engines.
// Engines that require keys but don't have them set are auto-skipped at runtime
// by resolvePlan (StatusMissingKey), so they appear in --list-engines as
// "missing-key" rather than silently vanishing.
func defaultRegistry() *engine.Registry {
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

// run is the testable core: parse flags → load config → resolve the engine plan →
// dispatch → rank → format → return the §4 exit code. All I/O goes through the
// provided readers/writers and the registry is injected, so behavior is fully
// exercisable without spawning the binary.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer, reg *engine.Registry) int {
	flags, err := cli.Parse(args, stderr)
	if err != nil {
		if errors.Is(err, cli.ErrHelp) {
			return 0 // usage already printed to stderr
		}
		emitError(stderr, format.ErrCodeUsage, err.Error(), 2)
		return 2
	}

	if flags.Version {
		fmt.Fprintf(stdout, "mst %s\n", version)
		return 0
	}

	cfg, err := config.Load()
	if err != nil {
		emitError(stderr, format.ErrCodeConfig, err.Error(), 2)
		return 2
	}

	// --schema / --help-json: emit the flag set as JSON for tool wrappers. Exit 0.
	if flags.Schema || flags.HelpJSON {
		if err := emitSchema(stdout, flags); err != nil {
			emitError(stderr, format.ErrCodeUsage, err.Error(), 2)
			return 2
		}
		return 0
	}

	// --dry-run: resolve config/flags/engines and print the plan without searching. Exit 0.
	if flags.DryRun {
		query, qerr := resolveQuery(flags, stdin)
		if qerr != nil {
			emitError(stderr, format.ErrCodeUsage, qerr.Error(), 2)
			return 2
		}
		dry, err := resolveDryRun(flags, cfg, reg)
		if err != nil {
			emitError(stderr, format.ErrCodeUsage, err.Error(), 2)
			return 2
		}
		dry.Query = query
		if err := emitDryRun(stdout, dry); err != nil {
			emitError(stderr, format.ErrCodeUsage, err.Error(), 2)
			return 2
		}
		return 0
	}

	// --list-engines short-circuits before query resolution: render the full §4
	// listing (name/enabled/keyed/key_present/weight/status) respecting --format.
	if flags.ListEngines {
		if verr := validateListFormat(flags.Format); verr != nil {
			emitError(stderr, format.ErrCodeUsage, verr.Error(), 2)
			return 2
		}
		entries := computeListEntries(flags, cfg, reg)
		out, err := format.MarshalListEngines(entries, flags.Format)
		if err != nil {
			emitError(stderr, format.ErrCodeUsage, err.Error(), 2)
			return 2
		}
		stdout.Write(out)
		return 0
	}

	query, qerr := resolveQuery(flags, stdin)
	if qerr != nil {
		emitError(stderr, format.ErrCodeUsage, qerr.Error(), 2)
		return 2
	}

	if verr := validateFormat(flags); verr != nil {
		emitError(stderr, format.ErrCodeUsage, verr.Error(), 2)
		return 2
	}

	p, perr := resolvePlan(flags, cfg, reg)
	if perr != nil {
		emitError(stderr, format.ErrCodeUsage, perr.Error(), 2)
		return 2
	}

	// Dispatch under a global deadline (§7).
	ctx, cancel := context.WithTimeout(context.Background(), p.globalTo)
	defer cancel()

	requireSet := setOf(flags.Require)

	// --raw: bypass normalization; emit each engine's parsed native payload
	// keyed by engine name (§10). JSON-only (--raw + text/table/jsonl is exit 2,
	// already rejected by validateFormat). --raw implies --no-merge, so this path
	// takes precedence when both are set.
	if flags.Raw {
		start := time.Now()
		runs := search.DispatchRaw(ctx, p.engines, query, p.fetchLimit, p.timeouts, p.engineOpts)
		tookMs := time.Since(start).Milliseconds()
		env := format.BuildRawEnvelope(query, runs, p.limit, tookMs)
		exit := computeExit(rawPayloadCount(runs), runs, requireSet, flags.Strict)
		emitNonZeroError(stderr, exit, runs, flags.Require, len(p.engines))
		out, merr := format.MarshalRaw(env)
		if merr != nil {
			emitError(stderr, format.ErrCodeUsage, merr.Error(), 2)
			return 2
		}
		stdout.Write(out)
		return exit
	}

	// --no-merge: skip ranking/dedup; emit per-engine native-order groups (§10).
	// --offset/--limit are merged-window controls, so they are no-ops here; §4
	// requires a -v notice when --offset is set under --no-merge.
	if flags.NoMerge {
		start := time.Now()
		runs := search.Dispatch(ctx, p.engines, query, p.fetchLimit, p.timeouts, p.engineOpts)
		tookMs := time.Since(start).Milliseconds()
		if p.offset != 0 {
			vlog(stderr, flags.Verbose, 1, "--offset is a no-op under --no-merge (results are not merged)")
		}
		env := format.BuildUnmergedEnvelope(query, runs, p.limit, tookMs)
		exit := computeExit(len(env.Results), runs, requireSet, flags.Strict)
		emitNonZeroError(stderr, exit, runs, flags.Require, len(p.engines))
		out, merr := format.MarshalUnmerged(env, flags.Format)
		if merr != nil {
			emitError(stderr, format.ErrCodeUsage, merr.Error(), 2)
			return 2
		}
		stdout.Write(out)
		return exit
	}

	// Merged path: dispatch → rank → window (§9).
	start := time.Now()
	runs := search.Dispatch(ctx, p.engines, query, p.fetchLimit, p.timeouts, p.engineOpts)
	results := rank.Window(rank.Rank(runs, p.weights, p.regOrder, p.rrfK), p.offset, p.limit)
	tookMs := time.Since(start).Milliseconds()

	// Extract the AI-generated answer from the tavily raw payload (if any).
	// The answer does NOT compose with RRF — it is surfaced as a separate
	// top-level field in the output envelope (§5 Tier D, Sprint D.1.2).
	answer := extractTavilyAnswer(runs)

	env := format.BuildEnvelope(query, runs, results, p.limit, tookMs, answer)

	exit := computeExit(len(results), runs, requireSet, flags.Strict)
	emitNonZeroError(stderr, exit, runs, flags.Require, len(p.engines))

	// --fields: project result fields before rendering.
	var out []byte
	var merr error
	if len(flags.Fields) > 0 {
		if err := validateFields(flags.Fields); err != nil {
			emitError(stderr, format.ErrCodeUsage, err.Error(), 2)
			return 2
		}
		out, merr = MarshalResults(results, flags.Format, flags.Fields,
			query, tookMs, p.limit, env.Engines, answer)
	} else {
		out, merr = format.Marshal(env, flags.Format)
	}
	if merr != nil {
		emitError(stderr, format.ErrCodeUsage, merr.Error(), 2)
		return 2
	}
	stdout.Write(out)
	return exit
}

// computeExit returns the §4 exit code from the run outcome. Precedence
// (highest first): 2 > 3 > 1 > 0. Exit 2 is decided upstream (before dispatch);
// this only distinguishes 0 / 1 / 3. Exit 3 = a required engine ran but failed,
// OR --strict with any engine failure; exit 3 beats exit 1 when both apply.
// resultCount is the number of output items produced (merged results, unmerged
// results, or raw payloads) — 0 ⇒ exit 1.
func computeExit(resultCount int, runs []engine.EngineRun, requireSet map[string]bool, strict bool) int {
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

// emitNonZeroError writes the §11 structured stderr object for a non-0/2 exit:
// exit 1 = no results (special-cased when no engine was ready), exit 3 = an
// engine failure. Exit 0 emits nothing. (Exit 2 is handled at the point of
// failure, before any output.)
func emitNonZeroError(stderr io.Writer, exit int, runs []engine.EngineRun, require []string, numEngines int) {
	switch exit {
	case 1:
		msg := "no results"
		if numEngines == 0 {
			msg = "no results: no ready engines (try --list-engines)"
		}
		emitError(stderr, format.ErrCodeNoResults, msg, 1)
	case 3:
		emitError(stderr, format.ErrCodeEngineFailure, engineFailureMessage(runs, require), 3)
	}
}

// vlog writes a -v diagnostic line to stderr when the verbose level is met
// (§13: diagnostics go to stderr, never stdout). Minimal hook for the few spec
// notices that require it (e.g. §4 --offset no-op under --no-merge); the fuller
// -v logging (per-engine latency/score breakdown) remains deferred.
func vlog(stderr io.Writer, verbose, level int, format string, a ...any) {
	if verbose >= level {
		fmt.Fprintf(stderr, "mst: "+format+"\n", a...)
	}
}

// rawPayloadCount returns the number of runs that produced a native payload
// (run.Raw != nil) — the "result count" used for exit-code purposes under --raw
// (§10): zero payloads ⇒ exit 1 (no results), subject to the usual exit-3 rule.
func rawPayloadCount(runs []engine.EngineRun) int {
	n := 0
	for _, r := range runs {
		if r.Raw != nil {
			n++
		}
	}
	return n
}

// engineFailureMessage builds a human-readable message for the exit-3 structured
// error, distinguishing a required-engine failure from a --strict failure.
func engineFailureMessage(runs []engine.EngineRun, require []string) string {
	requireSet := setOf(require)
	var failed, required []string
	for _, r := range runs {
		if r.Err == nil {
			continue
		}
		failed = append(failed, r.Engine)
		if requireSet[r.Engine] {
			required = append(required, r.Engine)
		}
	}
	if len(required) > 0 {
		return fmt.Sprintf("required engine(s) failed: %s", strings.Join(required, ", "))
	}
	return fmt.Sprintf("engine(s) failed (--strict): %s", strings.Join(failed, ", "))
}

// resolveQuery returns the query from positional args or --stdin (§4).
func resolveQuery(flags *cli.Flags, stdin io.Reader) (string, error) {
	if flags.Query != "" {
		return flags.Query, nil
	}
	if flags.Stdin {
		data, err := io.ReadAll(stdin)
		if err != nil {
			return "", fmt.Errorf("stdin read error: %w", err)
		}
		q := strings.TrimSpace(string(data))
		if q == "" {
			return "", fmt.Errorf("empty query from stdin")
		}
		return q, nil
	}
	return "", fmt.Errorf("no query provided (use positional args, --stdin, or -q/--query)")
}

// validateFormat checks the --format value and the --raw / --no-merge modes
// (§10). --no-merge works with json/jsonl/text/table. --raw is JSON-only; combined
// with text/table/jsonl it is a usage error (§10). --raw implies --no-merge, so
// both being set is allowed (--raw wins).
func validateFormat(flags *cli.Flags) error {
	switch flags.Format {
	case "json", "jsonl", "text", "table":
		// supported
	default:
		return fmt.Errorf("unknown --format %q (valid: json | jsonl | text | table)", flags.Format)
	}
	if flags.Raw && flags.Format != "json" {
		return fmt.Errorf("--raw is only compatible with --format json, got %s", flags.Format)
	}
	return nil
}

// validateListFormat checks --format for the --list-engines path (§4/§10):
// json/jsonl/text are supported; table is v0.2 (exit 2); anything else is
// unknown. (--raw/--no-merge do not apply to the engine listing and are ignored.)
func validateListFormat(format string) error {
	switch format {
	case "json", "jsonl", "text":
		return nil
	default:
		return fmt.Errorf("unknown --format %q (valid: json | jsonl | text)", format)
	}
}

// emitError writes the §11 structured error object to w.
func emitError(w io.Writer, code, message string, exit int) {
	w.Write(format.MarshalError(code, message, exit))
}

// extractTavilyAnswer extracts the AI-generated answer from the tavily engine's
// raw payload, if present. Returns empty string if tavily didn't run, didn't
// return an answer, or include_answer was not enabled. The answer is surfaced
// as a separate top-level field in the output envelope (§5 Tier D, Sprint D.1.2)
// and does NOT compose with RRF.
func extractTavilyAnswer(runs []engine.EngineRun) string {
	for _, r := range runs {
		if r.Engine == "tavily" && r.Raw != nil {
			return tavily.GetAnswerFromRaw(r.Raw)
		}
	}
	return ""
}
