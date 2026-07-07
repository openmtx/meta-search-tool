// mst — Meta-Search Tool.
//
// A command-line meta-search tool that fans out concurrent queries to multiple
// search engines, merges and re-ranks the results (RRF), and emits them as JSON
// (or jsonl/text) for an AI agent harness to consume. See docs/SPEC.md.
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
	"github.com/openmtx/meta-search-tool/internal/engine/brave"
	"github.com/openmtx/meta-search-tool/internal/format"
	"github.com/openmtx/meta-search-tool/internal/rank"
	"github.com/openmtx/meta-search-tool/internal/search"
)

const version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, defaultRegistry()))
}

// defaultRegistry builds the engine registry and registers all available engines.
// Engines that require keys but don't have them set are auto-skipped at runtime
// by resolvePlan (StatusMissingKey), so they appear in --list-engines as
// "missing-key" rather than silently vanishing.
func defaultRegistry() *engine.Registry {
	reg := engine.NewRegistry()
	if err := reg.Register(brave.New()); err != nil {
		panic("brave: " + err.Error()) // duplicate name — impossible at compile time
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

	// --list-engines short-circuits before query resolution (full §4 output lands
	// in A.2.2; this is a minimal placeholder).
	if flags.ListEngines {
		listEnginesPlaceholder(stdout, reg)
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

	// Dispatch under a global deadline (§7), then rank + window (§9).
	ctx, cancel := context.WithTimeout(context.Background(), p.globalTo)
	defer cancel()

	start := time.Now()
	runs := search.Dispatch(ctx, p.engines, query, p.fetchLimit, p.timeouts)
	results := rank.Window(rank.Rank(runs, p.weights, p.regOrder, p.rrfK), p.offset, p.limit)
	tookMs := time.Since(start).Milliseconds()

	env := format.BuildEnvelope(query, runs, results, p.limit, tookMs)

	exit := computeExit(results, runs, setOf(flags.Require), flags.Strict)
	switch exit {
	case 1:
		msg := "no results"
		if len(p.engines) == 0 {
			msg = "no results: no ready engines (try --list-engines)"
		}
		emitError(stderr, format.ErrCodeNoResults, msg, 1)
	case 3:
		emitError(stderr, format.ErrCodeEngineFailure, engineFailureMessage(runs, flags.Require), 3)
	}

	out, merr := format.Marshal(env, flags.Format)
	if merr != nil {
		emitError(stderr, format.ErrCodeUsage, merr.Error(), 2)
		return 2
	}
	stdout.Write(out)
	return exit
}

// computeExit returns the §4 exit code from the run outcome. Precedence (highest
// first): 2 > 3 > 1 > 0. Exit 2 is decided upstream (before dispatch); this only
// distinguishes 0 / 1 / 3. Exit 3 = a required engine ran but failed, OR --strict
// with any engine failure; exit 3 beats exit 1 when both apply.
func computeExit(results []rank.RankedResult, runs []engine.EngineRun, requireSet map[string]bool, strict bool) int {
	for _, r := range runs {
		if r.Err != nil && (requireSet[r.Engine] || strict) {
			return 3
		}
	}
	if len(results) == 0 {
		return 1
	}
	return 0
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
	return "", fmt.Errorf("no query provided (use positional args or --stdin)")
}

// validateFormat checks the --format value and the --raw / --no-merge modes
// against what A.0.2 supports (§10), surfacing clear exit-2 errors for the
// deferred cases rather than silently no-op'ing (decision 2026-07-07c/d).
func validateFormat(flags *cli.Flags) error {
	switch flags.Format {
	case "json", "jsonl", "text":
		// supported
	case "table":
		return fmt.Errorf("--format table is not yet implemented (planned for v0.2)")
	default:
		return fmt.Errorf("unknown --format %q (valid: json | jsonl | text)", flags.Format)
	}
	if flags.Raw && flags.Format != "json" {
		return fmt.Errorf("--raw is only compatible with --format json, got %s", flags.Format)
	}
	if flags.Raw {
		return fmt.Errorf("--raw is not yet implemented (planned for A.2.x)")
	}
	if flags.NoMerge {
		return fmt.Errorf("--no-merge is not yet implemented (planned for A.2.x)")
	}
	return nil
}

// listEnginesPlaceholder is the minimal A.0.2 --list-engines output (full §4
// output — name/enabled/keyed/key_present/weight/status per engine, respecting
// --format — lands in A.2.2 hardening).
func listEnginesPlaceholder(w io.Writer, reg *engine.Registry) {
	names := reg.Names()
	if len(names) == 0 {
		fmt.Fprintln(w, "(no engines registered)")
		return
	}
	for _, n := range names {
		fmt.Fprintln(w, n)
	}
}

// emitError writes the §11 structured error object to w.
func emitError(w io.Writer, code, message string, exit int) {
	w.Write(format.MarshalError(code, message, exit))
}
