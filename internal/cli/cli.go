// Package cli handles command-line flag parsing and binding.
package cli

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
)

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
// (joined by spaces) or from stdin (--stdin).
func Parse() (*Flags, error) {
	f := &Flags{
		Limit:   10,
		Format:  "json",
		RRFK:    60,
		Timeout: "8s",
	}

	fs := flag.NewFlagSet("mst", flag.ContinueOnError)

	fs.StringVar(&f.Format, "format", "json", "Output format: json | jsonl | text | table")
	fs.IntVar(&f.Limit, "limit", 10, "Max number of merged results to return")
	fs.IntVar(&f.Offset, "offset", 0, "Skip the first N merged results (best-effort pagination)")
	fs.StringVar(&f.Timeout, "timeout", "8s", "Global timeout for the run (e.g. 5s, 500ms)")
	fs.StringVar(&f.PerEngineTimeout, "per-engine-timeout", "", "Per-engine timeout (defaults to min(--timeout, 6s))")
	fs.IntVar(&f.RRFK, "rrf-k", 60, "RRF smoothing constant")
	fs.BoolVar(&f.Stdin, "stdin", false, "Read query from stdin")
	fs.StringVar(&f.Config, "config", "", "Path to explicit config file")
	fs.BoolVar(&f.ListEngines, "list-engines", false, "List all engines and exit")
	fs.BoolVar(&f.Version, "version", false, "Print version and exit")
	fs.BoolVar(&f.Strict, "strict", false, "Exit 3 if any engine failed")
	fs.BoolVar(&f.NoMerge, "no-merge", false, "Keep results grouped per-engine")
	fs.BoolVar(&f.Raw, "raw", false, "Emit raw per-engine payloads")

	// Repeatable flags.
	var enginesStr, excludeStr, requireStr string
	var weightStrs []string
	var verbose int

	fs.StringVar(&enginesStr, "engine", "", "Comma-separated engine whitelist")
	fs.StringVar(&excludeStr, "exclude", "", "Comma-separated engine blacklist")
	fs.StringVar(&requireStr, "require", "", "Comma-separated required engines")
	fs.Var(&weightFlag{values: &weightStrs}, "weight", "Per-engine weight override (repeatable)")
	fs.IntVar(&verbose, "verbose", 0, "Verbosity level (-v = 1, -vv = 2)")

	// Parse known flags.
	if err := fs.Parse(os.Args[1:]); err != nil {
		return nil, fmt.Errorf("flag parse error: %w", err)
	}

	f.Verbose = verbose
	f.Format = resolveFormat(f.Format, f.Raw)

	// Parse comma-separated lists.
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
		// Split on the first '=' to separate ENGINE from FLOAT, so both
		// --weight brave=2.0 and --weight=brave=2.0 parse the same way (§4).
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

// resolveFormat adjusts the format based on --raw.
func resolveFormat(format string, raw bool) string {
	if raw {
		return "json"
	}
	return format
}

// weightFlag implements flag.Value for repeatable --weight flags.
type weightFlag struct {
	values *[]string
}

func (f *weightFlag) String() string {
	// Guard against a nil receiver/values: Go's flag package reflects a
	// zero-value flag.Value and calls String() on it (in isZeroValue), which
	// would otherwise nil-deref and panic on every usage error.
	if f == nil || f.values == nil || *f.values == nil {
		return ""
	}
	return strings.Join(*f.values, ", ")
}

func (f *weightFlag) Set(s string) error {
	*f.values = append(*f.values, s)
	return nil
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
