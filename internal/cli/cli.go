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
	"io"
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
	Proxy            string // --proxy: per-run outbound proxy URL (§6)
	PerEngineTimeout string
	NoMerge          bool
	Raw              bool
	Config           string
	ListEngines      bool
	Verbose          int
	Version          bool
	DryRun           bool     // --dry-run: print resolved plan, exit 0
	Fields           []string // --fields: result field projection
	Schema           bool     // --schema: emit flag schema as JSON, exit 0
	HelpJSON         bool     // --help-json: alias for --schema
	QueryFromFlag    bool     // true when -q/--query flag was used (vs positional)
}

// Parse parses CLI flags from args. out receives --help usage text (errors are
// returned, not printed, so the caller can emit them as the §11 structured
// object). Short aliases per §4: -e/--engine, -n/--limit, -f/--format,
// -v/--verbose (repeatable count).
func Parse(args []string, out io.Writer) (*Flags, error) {
	f := &Flags{
		Limit:   10,
		Format:  "json",
		RRFK:    60,
		Timeout: "8s",
	}

	fs := pflag.NewFlagSet("mst", pflag.ContinueOnError)
	// Suppress pflag's own error/usage printing: run() emits every diagnostic as the
	// §11 structured object, so stderr never carries two formats at once. --help
	// usage is printed explicitly below.
	fs.SetOutput(io.Discard)
	// pflag leaves FlagSet.Usage nil by default (calling it panics); assign a
	// real printer so we can invoke it on --help. §4 requires --help to group
	// flags by skill tier; groupedUsage regroups pflag's own (correctly
	// type/default-formatted) per-flag lines into the §4 tiers, in spec order.
	fs.Usage = func() { groupedUsage(fs) }

	var enginesStr, excludeStr, requireStr, queryFlag string
	var weightStrs []string
	var queryChanged bool

	// Tier 1 — skill-facing (§4).
	fs.StringVarP(&f.Format, "format", "f", "json", "Output format: json | jsonl | text | table")
	fs.StringArrayVar(&f.Fields, "fields", nil, "Result field projection (repeatable; e.g. --fields url title snippet)")
	fs.IntVarP(&f.Limit, "limit", "n", 10, "Max number of merged results to return")
	fs.IntVar(&f.Offset, "offset", 0, "Skip the first N merged results (best-effort pagination)")
	fs.BoolVar(&f.Stdin, "stdin", false, "Read query from stdin")
	fs.StringVarP(&enginesStr, "engine", "e", "", "Comma-separated engine whitelist (e.g. brave,ddg)")
	fs.StringVar(&excludeStr, "exclude", "", "Comma-separated engines to disable for this run")
	fs.StringArrayVarP(&weightStrs, "weight", "", nil, "Per-engine weight override ENGINE=FLOAT (repeatable)")
	fs.BoolVar(&f.ListEngines, "list-engines", false, "List every engine (name/status/weight) and exit")
	fs.BoolVar(&f.Version, "version", false, "Print version and exit")

	// -q/--query: the search query as a flag value. Also accepted as positional
	// args (joined by spaces). Specifying both --query and positional args is a
	// usage error (same as --stdin + positional).
	fs.StringVarP(&queryFlag, "query", "q", "", "Search query (also accepted as positional args)")

	// Tier 2 — tuning.
	fs.StringVar(&requireStr, "require", "", "Comma-separated engines that must run")
	fs.BoolVar(&f.Strict, "strict", false, "Exit 3 if any engine that ran failed or timed out")
	fs.IntVar(&f.RRFK, "rrf-k", 60, "RRF smoothing constant; smaller = more top-heavy")
	fs.StringVar(&f.Timeout, "timeout", "8s", "Global timeout for the run (e.g. 5s, 500ms)")
	fs.StringVar(&f.Proxy, "proxy", "", "Route outbound engine requests through a proxy URL (http, https, socks5, socks5h)")

	// Tier 3 — debug/internal.
	fs.StringVar(&f.PerEngineTimeout, "per-engine-timeout", "", "Per-engine timeout (defaults to min(--timeout, 6s))")
	fs.BoolVar(&f.NoMerge, "no-merge", false, "Keep results grouped per-engine instead of merged")
	fs.BoolVar(&f.Raw, "raw", false, "Emit raw per-engine payloads without normalization (JSON only)")
	fs.StringVar(&f.Config, "config", "", "Path to an explicit config file (overrides discovery)")
	fs.CountVarP(&f.Verbose, "verbose", "v", "Verbosity level (-v = 1, -vv = 2)")

	// Tier 3 — programmatic/inspection affordances (v0.3): --schema/--help-json
	// emit the flag set as JSON for tool wrappers; --dry-run previews the plan.
	// (--fields is skill-facing; registered with Tier 1 above.)
	fs.BoolVar(&f.DryRun, "dry-run", false, "Resolve config/flags/engines and print the plan without searching")
	fs.BoolVar(&f.Schema, "schema", false, "Emit the flag set as JSON for tool wrappers")
	fs.BoolVar(&f.HelpJSON, "help-json", false, "Alias for --schema (emit the flag set as JSON)")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, pflag.ErrHelp) {
			fs.SetOutput(out)
			fs.Usage()
			return f, ErrHelp
		}
		return nil, err
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

	// Track whether -q/--query was explicitly used (via pflag's Changed).
	queryChanged = fs.Changed("query")

	// Parse positional args as query.
	positionalArgs := fs.Args()

	// Conflict: --query + positional args is a usage error (§4).
	if queryChanged && len(positionalArgs) > 0 {
		return nil, fmt.Errorf("specifying both --query and positional args is an error")
	}
	// Conflict: --query + --stdin is a usage error (two query sources).
	if queryChanged && f.Stdin {
		return nil, fmt.Errorf("specifying both --query and --stdin is an error")
	}
	// Conflict: --stdin + positional args is a usage error.
	if f.Stdin && len(positionalArgs) > 0 {
		return nil, fmt.Errorf("specifying both --stdin and positional args is an error")
	}

	if queryChanged {
		// Query from -q/--query flag.
		if strings.TrimSpace(queryFlag) == "" {
			return nil, fmt.Errorf("empty query")
		}
		f.Query = queryFlag
		f.QueryFromFlag = true
	} else if !f.Stdin {
		// Query from positional args.
		query := strings.Join(positionalArgs, " ")
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

// helpTiers is the §4 skill-tier classification, in display order. Each entry
// is one tier's (header, ordered flag names).
var helpTiers = []struct {
	header string
	flags  []string
}{
	{
		header: "Tier 1 — skill-facing (stable; the only flags a SKILL.md should document)",
		flags:  []string{"query", "engine", "exclude", "limit", "offset", "format", "fields", "weight", "stdin", "list-engines", "version"},
	},
	{
		header: "Tier 2 — tuning (documented, discouraged for routine skill use)",
		flags:  []string{"rrf-k", "require", "strict", "timeout", "proxy"},
	},
	{
		header: "Tier 3 — debug/internal (humans; an LLM should not need these)",
		flags:  []string{"raw", "no-merge", "per-engine-timeout", "config", "verbose", "dry-run", "schema", "help-json"},
	},
}

// groupedUsage prints the --help text with flags grouped by §4 skill tier. It
// delegates per-flag formatting (type placeholder, default value, alignment) to
// pflag's own FlagUsagesWrapped so the output stays consistent with pflag's
// conventions — it only reorders and re-groups those ready-made lines into the
// tiered sections, preserving spec order within each tier.
func groupedUsage(fs *pflag.FlagSet) {
	out := fs.Output()
	fmt.Fprintf(out, "Usage:\n  mst [flags] <query...>\n")
	fmt.Fprintf(out, "\n<query> is the search query (positional args, joined by spaces); ")
	fmt.Fprintf(out, "use -q/--query to specify it as a flag.\n")

	// One formatted line per flag, keyed by flag name. A wide wrap disables
	// usage wrapping so each flag is exactly one line.
	lines := map[string]string{}
	for _, line := range strings.Split(strings.TrimRight(fs.FlagUsagesWrapped(1<<30), "\n"), "\n") {
		line = strings.TrimRight(line, " ")
		if line == "" {
			continue
		}
		if name := flagNameInUsage(line); name != "" {
			lines[name] = line
		}
	}

	for _, tier := range helpTiers {
		fmt.Fprintf(out, "\n%s:\n", tier.header)
		for _, name := range tier.flags {
			if line, ok := lines[name]; ok {
				fmt.Fprintln(out, line)
			}
		}
	}
}

// flagNameInUsage extracts the long flag name from one pflag usage line, e.g.
// "  -e, --engine string   ..." → "engine"; "      --stdin   ..." → "stdin".
func flagNameInUsage(line string) string {
	i := strings.Index(line, "--")
	if i < 0 {
		return ""
	}
	rest := line[i+2:]
	// The name runs until the next whitespace.
	j := 0
	for j < len(rest) && (rest[j] == '-' || (rest[j] >= 'a' && rest[j] <= 'z') || (rest[j] >= 'A' && rest[j] <= 'Z') || (rest[j] >= '0' && rest[j] <= '9')) {
		j++
	}
	return rest[:j]
}
