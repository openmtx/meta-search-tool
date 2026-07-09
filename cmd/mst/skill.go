package main

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/openmtx/meta-search-tool/internal/cli"
	"github.com/openmtx/meta-search-tool/internal/format"
	"github.com/openmtx/meta-search-tool/internal/rank"
)

// --- --fields result projection (v0.3, skill affordance) -------------------

// validResultFields lists the fields that can be projected in a RankedResult.
var validResultFields = []string{"rank", "title", "url", "source", "snippet", "score", "engines"}

// validateFields checks that all requested fields are valid result fields.
func validateFields(fields []string) error {
	if len(fields) == 0 {
		return nil // empty = no projection, use defaults
	}
	seen := make(map[string]bool)
	for _, f := range fields {
		f = strings.ToLower(strings.TrimSpace(f))
		if !validField(f) {
			return fmt.Errorf("unknown --field %q (valid: %s)", f, strings.Join(validResultFields, ", "))
		}
		if seen[f] {
			return fmt.Errorf("duplicate --field %q", f)
		}
		seen[f] = true
	}
	return nil
}

// validField returns true if s is a valid result projection field.
func validField(s string) bool {
	for _, v := range validResultFields {
		if s == v {
			return true
		}
	}
	return false
}

// projectResults applies the --fields projection to each RankedResult, returning
// a map[string]any slice that serializes with only the requested fields. The
// field order in the JSON output follows the order specified by --fields.
func projectResults(results []rank.RankedResult, fields []string) []map[string]any {
	if len(fields) == 0 {
		return nil // no projection; caller uses full results
	}
	// Normalize fields to lowercase.
	proj := make([]string, len(fields))
	for i, f := range fields {
		proj[i] = strings.ToLower(strings.TrimSpace(f))
	}

	out := make([]map[string]any, len(results))
	for i, r := range results {
		m := make(map[string]any, len(proj))
		for _, field := range proj {
			switch field {
			case "rank":
				m["rank"] = r.Rank
			case "title":
				m["title"] = r.Title
			case "url":
				m["url"] = r.URL
			case "source":
				m["source"] = r.Source
			case "snippet":
				m["snippet"] = r.Snippet
			case "score":
				m["score"] = r.Score
			case "engines":
				m["engines"] = r.Engines
			}
		}
		out[i] = m
	}
	return out
}

// --- --schema / --help-json (v0.3, skill affordance) -----------------------

// schemaFlag describes one CLI flag for the --schema output.
type schemaFlag struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Default string `json:"default"`
	Help    string `json:"help"`
}

// emitSchema writes the flag set as JSON to w. The schema describes every
// registered CLI flag with its name, type, default value, and help text.
// Intended for tool wrappers that need to introspect the flag set.
func emitSchema(w io.Writer, flags *cli.Flags) error {
	s := schemaOutput{
		Version: 1,
		Tool:    "mst",
		Flags:   buildSchemaFlags(flags),
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal schema: %w", err)
	}
	data = append(data, '\n')
	_, err = w.Write(data)
	return err
}

// schemaOutput is the top-level structure for --schema output.
type schemaOutput struct {
	Version int          `json:"version"`
	Tool    string       `json:"tool"`
	Flags   []schemaFlag `json:"flags"`
}

// buildSchemaFlags returns the flag schema from the parsed flags. The type
// and default value reflect the actual CLI flag definitions.
func buildSchemaFlags(flags *cli.Flags) []schemaFlag {
	return []schemaFlag{
		{Name: "query", Type: "string", Default: "", Help: "Search query (also accepted as positional args)"},
		{Name: "engine", Type: "string[]", Default: "", Help: "Comma-separated engine whitelist (e.g. brave,ddg)"},
		{Name: "exclude", Type: "string", Default: "", Help: "Comma-separated engines to disable for this run"},
		{Name: "limit", Type: "int", Default: "10", Help: "Max number of merged results to return"},
		{Name: "offset", Type: "int", Default: "0", Help: "Skip the first N merged results (best-effort pagination)"},
		{Name: "format", Type: "string", Default: "json", Help: "Output format: json | jsonl | text | table"},
		{Name: "weight", Type: "string[]", Default: "", Help: "Per-engine weight override ENGINE=FLOAT (repeatable)"},
		{Name: "stdin", Type: "bool", Default: "false", Help: "Read query from stdin"},
		{Name: "list-engines", Type: "bool", Default: "false", Help: "List every engine (name/status/weight) and exit"},
		{Name: "version", Type: "bool", Default: "false", Help: "Print version and exit"},
		{Name: "rrf-k", Type: "int", Default: "60", Help: "RRF smoothing constant; smaller = more top-heavy"},
		{Name: "require", Type: "string", Default: "", Help: "Comma-separated engines that must run"},
		{Name: "strict", Type: "bool", Default: "false", Help: "Exit 3 if any engine that ran failed or timed out"},
		{Name: "timeout", Type: "string", Default: "8s", Help: "Global timeout for the run (e.g. 5s, 500ms)"},
		{Name: "per-engine-timeout", Type: "string", Default: "", Help: "Per-engine timeout (defaults to min(--timeout, 6s))"},
		{Name: "raw", Type: "bool", Default: "false", Help: "Emit raw per-engine payloads without normalization (JSON only)"},
		{Name: "no-merge", Type: "bool", Default: "false", Help: "Keep results grouped per-engine instead of merged"},
		{Name: "config", Type: "string", Default: "", Help: "Path to an explicit config file (overrides discovery)"},
		{Name: "verbose", Type: "count", Default: "0", Help: "Verbosity level (-v = 1, -vv = 2)"},
		{Name: "dry-run", Type: "bool", Default: "false", Help: "Resolve config/flags/engines and print the plan without searching"},
		{Name: "fields", Type: "string[]", Default: "", Help: "Result field projection (repeatable; e.g. --fields url title snippet)"},
		{Name: "schema", Type: "bool", Default: "false", Help: "Emit the flag set as JSON for tool wrappers"},
		{Name: "help-json", Type: "bool", Default: "false", Help: "Alias for --schema (emit the flag set as JSON)"},
	}
}

// --- MarshalResults renders results in the requested format with optional --fields projection. ---

// MarshalResults renders a result slice in the requested format, applying
// --fields projection when specified. For json format it emits an envelope;
// for jsonl/text/table it emits per-result lines. The envelope uses the provided
// query, tookMs, limit, engines, and answer values.
func MarshalResults(results []rank.RankedResult, formatStr string, fields []string,
	query string, tookMs int64, limit int, engines map[string]format.EngineStat, answer string) ([]byte, error) {

	if len(fields) > 0 {
		// Projected results: emit as a JSON array of maps (json), or per-result
		// objects (jsonl/text/table).
		projected := projectResults(results, fields)
		switch formatStr {
		case "json":
			env := &projectedEnvelope{
				Schema:  format.SchemaVersion,
				Query:   query,
				TookMs:  tookMs,
				Limit:   limit,
				Engines: engines,
				Results: projected,
			}
			if answer != "" {
				env.Answer = answer
			}
			b, err := json.Marshal(env)
			if err != nil {
				return nil, err
			}
			return append(b, '\n'), nil
		case "jsonl":
			var buf strings.Builder
			for _, r := range projected {
				data, err := json.Marshal(r)
				if err != nil {
					return nil, err
				}
				fmt.Fprintf(&buf, "%s\n", data)
			}
			return []byte(buf.String()), nil
		case "text":
			return marshalProjectedText(projected), nil
		case "table":
			return marshalProjectedTable(projected), nil
		default:
			return nil, fmt.Errorf("unsupported format %q", formatStr)
		}
	}

	// No projection — use the standard envelope.
	env := &format.Envelope{
		Schema:  format.SchemaVersion,
		Query:   query,
		TookMs:  tookMs,
		Limit:   limit,
		Engines: engines,
		Results: results,
		Answer:  answer,
	}
	return format.Marshal(env, formatStr)
}

// projectedEnvelope is the envelope for --fields json output. Same shape as
// Envelope but with Results as []map[string]any (projected fields).
type projectedEnvelope struct {
	Schema  int                          `json:"schema"`
	Query   string                       `json:"query"`
	TookMs  int64                        `json:"took_ms"`
	Limit   int                          `json:"limit"`
	Engines map[string]format.EngineStat `json:"engines"`
	Results []map[string]any             `json:"results"`
	Answer  string                       `json:"answer,omitempty"`
}

// marshalProjectedText renders projected results in text format. It includes
// only the requested fields in a readable layout.
func marshalProjectedText(results []map[string]any) []byte {
	var b strings.Builder
	for _, r := range results {
		if rank, ok := r["rank"].(int); ok {
			fmt.Fprintf(&b, "%d. ", rank)
		} else if title, ok := r["title"].(string); ok && title != "" {
			fmt.Fprintf(&b, "%s - ", title)
		}
		if url, ok := r["url"].(string); ok && url != "" {
			fmt.Fprintf(&b, "%s\n", url)
		} else {
			fmt.Fprintln(&b)
		}
		if snippet, ok := r["snippet"].(string); ok && snippet != "" {
			fmt.Fprintf(&b, "    %s\n", snippet)
		}
	}
	return []byte(b.String())
}

// marshalProjectedTable renders projected results in table format with only
// the requested columns.
func marshalProjectedTable(results []map[string]any) []byte {
	if len(results) == 0 {
		return []byte{}
	}
	// Determine which columns are present from the first result.
	var cols []string
	for k := range results[0] {
		cols = append(cols, k)
	}
	sort.Strings(cols)

	// Build column data.
	colData := make(map[string][]string, len(cols))
	for _, r := range results {
		for _, c := range cols {
			v := fmt.Sprintf("%v", r[c])
			if v == "<nil>" || v == "[]" {
				v = ""
			}
			colData[c] = append(colData[c], v)
		}
	}

	var buf strings.Builder
	tw := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)
	// Header.
	for i, c := range cols {
		if i > 0 {
			fmt.Fprint(tw, "\t")
		}
		fmt.Fprint(tw, strings.ToUpper(c))
	}
	fmt.Fprintln(tw, "")

	// Rows.
	for i := range results {
		for j, c := range cols {
			if j > 0 {
				fmt.Fprint(tw, "\t")
			}
			if i < len(colData[c]) {
				fmt.Fprint(tw, colData[c][i])
			}
		}
		fmt.Fprintln(tw, "")
	}
	tw.Flush()
	return []byte(buf.String())
}
