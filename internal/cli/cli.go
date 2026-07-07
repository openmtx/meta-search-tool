// Package cli handles command-line flag parsing and binding.
//
// Flags are parsed with spf13/pflag (decision 2026-07-07) so the CLI can offer
// the short aliases and repeatable count flag documented in spec §4 —
// -e/--engine, -n/--limit, -f/--format, and -v/-vv for verbosity — none of
// which Go's stdlib flag package can express.
package cli

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/pflag"
)

// ErrHelp signals that the user requested --help; callers should exit 0.
// (pflag has already printed usage to stderr.)
var ErrHelp = errors.New("help requested")

// Flags holds all parsed CLI flags.
type Flags struct {
	Query            string
	Stdin            bool
	Engines          []string // comma-separated whitelist
	Exclude          []string // comma-separated blacklist
	Limit            int
	Offset           int
	Format           string
	Weight           map[string]float64 // engine -> weight override
	RRFK             int
	Require          []string
	Strict           bool
	Timeout          string
	PerEngineTimeout string
	NoMerge          bool
	Raw              bool
	Config           string
	ListEngines      bool
	Verbose          int
	Version          bool
}

// Parse parses the CLI flags. The query is taken from positional arguments
// (joined by single spaces) or from stdin (--stdin). Short aliases per §4:
// -e/--engine, -n/--limit, -f/--format, -v/--verbose (repeatable count).
func Parse() (*Flags, error) {
	f := &Flags{
		Limit:   10,
		Format:  "json",
		RRFK:    60,
		Timeout: "8s",
	}

	fs := pflag.NewFlagSet("mst", pflag.ContinueOnError)

	var enginesStr, excludeStr, requireStr string
	var weightStrs []string

	// Tier 1 — skill-facing (§4).
	fs.StringVarP(&f.Format, "format", "f", "json", "Output format: json | jsonl | text | table")
	fs.IntVarP(&f.Limit, "limit", "n", 10, "Max number of merged results to return")
	fs.IntVar(&f.Offset, "offset", 0, "Skip the first N merged results (best-effort pagination)")
	fs.BoolVar(&f.Stdin, "stdin", false, "Read query from stdin")
	fs.StringVarP(&enginesStr, "engine", "e", "", "Comma-separated engine whitelist (e.g. brave,ddg)")
	fs.StringVar(&excludeStr, "exclude", "", "Comma-separated engines to disable for this run")
	fs.StringArrayVarP(&weightStrs, "weight", "", nil, "Per-engine weight override ENGINE=FLOAT (repeatable)")
	fs.BoolVar(&f.ListEngines, "list-engines", false, "List every engine (name/status/weight) and exit")
	fs.BoolVar(&f.Version, "version", false, "Print version and exit")

	// Tier 2 — tuning.
	fs.StringVar(&requireStr, "require", "", "Comma-separated engines that must run")
	fs.BoolVar(&f.Strict, "strict", false, "Exit 3 if any engine that ran failed or timed out")
	fs.IntVar(&f.RRFK, "rrf-k", 60, "RRF smoothing constant; smaller = more top-heavy")
	fs.StringVar(&f.Timeout, "timeout", "8s", "Global timeout for the run (e.g. 5s, 500ms)")

	// Tier 3 — debug/internal.
	fs.StringVar(&f.PerEngineTimeout, "per-engine-timeout", "", "Per-engine timeout (defaults to min(--timeout, 6s))")
	fs.BoolVar(&f.NoMerge, "no-merge", false, "Keep results grouped per-engine instead of merged")
	fs.BoolVar(&f.Raw, "raw", false, "Emit raw per-engine payloads without normalization (JSON only)")
	fs.StringVar(&f.Config, "config", "", "Path to an explicit config file (overrides discovery)")
	fs.CountVarP(&f.Verbose, "verbose", "v", "Verbosity level (-v = 1, -vv = 2)")

	if err := fs.Parse(os.Args[1:]); err != nil {
		if errors.Is(err, pflag.ErrHelp) {
			return f, ErrHelp
		}
		return nil, fmt.Errorf("flag parse error: %w", err)
	}

	if enginesStr != "" {
		f.Engines = splitComma(enginesStr)
	}
	if excludeStr != "" {
		f.Exclude = splitComma(excludeStr)
	}
	if requireStr != "" {
		f.Require = splitComma(requireStr)
	}

	// Parse weight overrides.
	f.Weight = make(map[string]float64)
	for _, ws := range weightStrs {
		// Split on the first '=' so both --weight brave=2.0 and --weight=brave=2.0
		// parse the same way (§4).
		parts := strings.SplitN(ws, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid weight %q (expected ENGINE=FLOAT)", ws)
		}
		name, val := parts[0], parts[1]
		v, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid weight %q for engine %q: expected a number", val, name)
		}
		f.Weight[name] = v
	}

	// Parse positional args as query.
	positionalArgs := fs.Args()
	if f.Stdin && len(positionalArgs) > 0 {
		return nil, fmt.Errorf("specifying both --stdin and positional args is an error")
	}
	query := strings.Join(positionalArgs, " ")
	if !f.Stdin {
		// A positional query that is empty/whitespace-only is a usage error (§4).
		if len(positionalArgs) > 0 && strings.TrimSpace(query) == "" {
			return nil, fmt.Errorf("empty query")
		}
		f.Query = query
	}
	// f.Stdin: the query is read from stdin later in run().

	return f, nil
}

// splitComma splits a comma-separated string, trimming whitespace and filtering empty.
func splitComma(s string) []string {
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}
