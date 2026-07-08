// Package format renders the merged result set and per-engine run metadata as the
// output shapes defined in §10 (json / jsonl / text envelopes) and the structured
// stderr error object defined in §11. It is pure: it turns in-memory values into
// bytes; it does no I/O of its own.
package format

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/openmtx/meta-search-tool/internal/engine"
	"github.com/openmtx/meta-search-tool/internal/rank"
)

// SchemaVersion is the output envelope version (§10). Bumped only on breaking
// changes to the envelope/result shape; additive fields do not bump it.
const SchemaVersion = 1

// Structured-error codes (§11).
const (
	ErrCodeUsage         = "usage"
	ErrCodeConfig        = "config"
	ErrCodeNoResults     = "no_results"
	ErrCodeEngineFailure = "engine_failure"
)

// EngineStat is one engine's execution summary in the `engines` block (§10).
type EngineStat struct {
	OK     bool   `json:"ok"`
	Count  int    `json:"count"`
	TookMs int64  `json:"took_ms"`
	Error  string `json:"error"`
}

// Envelope is the §10 `json` output envelope. It carries the query, the whole-run
// wall-clock latency, the per-engine stats, and the ranked/windowed results.
type Envelope struct {
	Schema  int                   `json:"schema"`
	Query   string                `json:"query"`
	TookMs  int64                 `json:"took_ms"`
	Limit   int                   `json:"limit"`
	Engines map[string]EngineStat `json:"engines"`
	Results []rank.RankedResult   `json:"results"`
}

// StructuredError is the §11 stderr object emitted on every non-zero exit:
// `{"error":{"code":"…","message":"…","exit":N}}`.
type StructuredError struct {
	Error ErrorBody `json:"error"`
}

// ErrorBody is the inner body of StructuredError.
type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Exit    int    `json:"exit"`
}

// BuildEnvelope assembles the §10 envelope from the dispatch runs and the
// ranked/windowed results. The engines block is built from runs: ok = no error,
// count = len(results), took_ms = the call latency, error = normalized message.
// Empty results are normalized to `[]` (never JSON null) so exit-1 stdout is a
// valid `results: []` payload per §4.
func BuildEnvelope(query string, runs []engine.EngineRun, results []rank.RankedResult, limit int, tookMs int64) *Envelope {
	if results == nil {
		results = []rank.RankedResult{}
	}
	return &Envelope{
		Schema:  SchemaVersion,
		Query:   query,
		TookMs:  tookMs,
		Limit:   limit,
		Engines: buildEngineStats(runs),
		Results: results,
	}
}

// buildEngineStats renders the per-engine §10 `engines` block from the dispatch
// runs (shared by the merged, --no-merge, and --raw envelopes).
func buildEngineStats(runs []engine.EngineRun) map[string]EngineStat {
	engines := make(map[string]EngineStat, len(runs))
	for _, r := range runs {
		engines[r.Engine] = EngineStat{
			OK:     r.Err == nil,
			Count:  len(r.Results),
			TookMs: r.Latency.Milliseconds(),
			Error:  errString(r.Err),
		}
	}
	return engines
}

// Marshal renders the envelope in the requested format (§10). json → the full
// envelope (compact); jsonl → one result object per line, no envelope; text →
// "rank. title - url\n    snippet" per result. All formats end with a trailing
// newline. table/raw/--no-merge are handled (or rejected) upstream; an unknown
// format here is an error.
func Marshal(env *Envelope, format string) ([]byte, error) {
	switch format {
	case "json":
		b, err := json.Marshal(env)
		if err != nil {
			return nil, err
		}
		return append(b, '\n'), nil
	case "jsonl":
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		for i := range env.Results {
			if err := enc.Encode(&env.Results[i]); err != nil {
				return nil, err
			}
		}
		return buf.Bytes(), nil
	case "text":
		return marshalText(env.Results), nil
	default:
		return nil, fmt.Errorf("unsupported format %q", format)
	}
}

// marshalText renders the §10 text format: one block per result,
//
//	RANK. TITLE - URL
//	    SNIPPET
//
// (the indented snippet line is omitted when the result has no snippet).
func marshalText(results []rank.RankedResult) []byte {
	var b strings.Builder
	for _, r := range results {
		fmt.Fprintf(&b, "%d. %s - %s\n", r.Rank, r.Title, r.URL)
		if r.Snippet != "" {
			fmt.Fprintf(&b, "    %s\n", r.Snippet)
		}
	}
	return []byte(b.String())
}

// ListEngine is one row of the --list-engines output (§4): the fully-resolved
// state of one registered engine, so an agent can programmatically select a
// working engine set. Each entry reports name, enabled (config/flag state),
// keyed, key_present (true/false for keyed engines, or null for keyless), the
// effective weight (after config + flag merge), and status ∈
// {ready, disabled, missing-key}. Discovery/precedence is identical to a normal
// run (§6), computed by the caller (cmd/mst.computeListEntries).
type ListEngine struct {
	Name       string  `json:"name"`
	Enabled    bool    `json:"enabled"`
	Keyed      bool    `json:"keyed"`
	KeyPresent *bool   `json:"key_present"` // nil (→ null) for keyless engines
	Weight     float64 `json:"weight"`
	Status     string  `json:"status"`
}

// MarshalListEngines renders the --list-engines listing in the requested format
// (§4): json → a compact array of entries; jsonl → one engine per line; text →
// a human-readable aligned summary. table is v0.2 (rejected as an error so the
// caller surfaces a clear exit-2). An empty registry serializes as `[]` (json)
// / empty (jsonl) / a placeholder line (text), never JSON null.
func MarshalListEngines(engines []ListEngine, format string) ([]byte, error) {
	if engines == nil {
		engines = []ListEngine{} // normalize so JSON is `[]`, never null
	}
	switch format {
	case "json":
		b, err := json.Marshal(engines)
		if err != nil {
			return nil, err
		}
		return append(b, '\n'), nil
	case "jsonl":
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		for i := range engines {
			if err := enc.Encode(&engines[i]); err != nil {
				return nil, err
			}
		}
		return buf.Bytes(), nil
	case "text":
		return marshalListText(engines), nil
	default:
		return nil, fmt.Errorf("unsupported format %q for --list-engines", format)
	}
}

// marshalListText renders the §4 human-readable --list-engines summary with
// aligned columns (tabwriter): NAME, ENABLED, KEYED, KEY, WEIGHT, STATUS.
func marshalListText(engines []ListEngine) []byte {
	if len(engines) == 0 {
		return []byte("(no engines registered)\n")
	}
	var buf bytes.Buffer
	tw := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tENABLED\tKEYED\tKEY\tWEIGHT\tSTATUS")
	for _, e := range engines {
		key := "keyless"
		if e.Keyed {
			if e.KeyPresent != nil && *e.KeyPresent {
				key = "present"
			} else {
				key = "absent"
			}
		}
		fmt.Fprintf(tw, "%s\t%v\t%v\t%s\t%g\t%s\n", e.Name, e.Enabled, e.Keyed, key, e.Weight, e.Status)
	}
	tw.Flush()
	return buf.Bytes()
}

// --- --no-merge output (§10) ----------------------------------------------

// UnmergedResult is one result in --no-merge output (§10): a raw engine result
// in engine-native order, tagged with its engine, with no score/rank. Source is
// still derived from the URL (deterministic) for consistency with the merged
// output; the spec only drops score/rank and adds `engine`.
type UnmergedResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Source  string `json:"source"`
	Snippet string `json:"snippet,omitempty"`
	Engine  string `json:"engine"`
}

// UnmergedEnvelope is the §10 --no-merge envelope: the same scaffolding as the
// merged envelope (schema/query/latency/engines) but with results grouped
// per-engine in native order, each carrying an `engine` field and no score/rank.
type UnmergedEnvelope struct {
	Schema  int                   `json:"schema"`
	Query   string                `json:"query"`
	TookMs  int64                 `json:"took_ms"`
	Limit   int                   `json:"limit"`
	Engines map[string]EngineStat `json:"engines"`
	Results []UnmergedResult      `json:"results"`
}

// BuildUnmerged flattens dispatch runs into per-engine native-order results
// (§10 --no-merge): registration order across engines, native rank order within
// each engine. A run's Results are included regardless of Err (partial results
// from a mid-pagination failure still surface, mirroring the merged path). The
// returned slice is never nil so JSON serializes as `[]`.
func BuildUnmerged(runs []engine.EngineRun) []UnmergedResult {
	out := make([]UnmergedResult, 0)
	for _, run := range runs {
		for _, r := range run.Results {
			out = append(out, UnmergedResult{
				Title:   r.Title,
				URL:     r.URL,
				Source:  rank.SourceFromURL(r.URL),
				Snippet: r.Snippet,
				Engine:  run.Engine,
			})
		}
	}
	return out
}

// BuildUnmergedEnvelope assembles the §10 --no-merge envelope.
func BuildUnmergedEnvelope(query string, runs []engine.EngineRun, limit int, tookMs int64) *UnmergedEnvelope {
	return &UnmergedEnvelope{
		Schema:  SchemaVersion,
		Query:   query,
		TookMs:  tookMs,
		Limit:   limit,
		Engines: buildEngineStats(runs),
		Results: BuildUnmerged(runs),
	}
}

// MarshalUnmerged renders the --no-merge envelope (§10): json → the full
// envelope; jsonl → one unmerged result per line; text → "ENGINE: title - url"
// per result (the engine name prefixes each entry and the rank column is
// dropped, per §10). table is v0.2 (rejected as an error).
func MarshalUnmerged(env *UnmergedEnvelope, format string) ([]byte, error) {
	switch format {
	case "json":
		b, err := json.Marshal(env)
		if err != nil {
			return nil, err
		}
		return append(b, '\n'), nil
	case "jsonl":
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		for i := range env.Results {
			if err := enc.Encode(&env.Results[i]); err != nil {
				return nil, err
			}
		}
		return buf.Bytes(), nil
	case "text":
		return marshalUnmergedText(env.Results), nil
	default:
		return nil, fmt.Errorf("unsupported format %q for --no-merge", format)
	}
}

// marshalUnmergedText renders the §10 --no-merge text format: the engine name
// prefixes each entry and there is no rank column.
//
//	ENGINE: TITLE - URL
//	    SNIPPET
func marshalUnmergedText(results []UnmergedResult) []byte {
	var b strings.Builder
	for _, r := range results {
		fmt.Fprintf(&b, "%s: %s - %s\n", r.Engine, r.Title, r.URL)
		if r.Snippet != "" {
			fmt.Fprintf(&b, "    %s\n", r.Snippet)
		}
	}
	return []byte(b.String())
}

// MarshalError renders the §11 structured stderr object for a non-zero exit.
func MarshalError(code, message string, exit int) []byte {
	b, err := json.Marshal(StructuredError{Error: ErrorBody{Code: code, Message: message, Exit: exit}})
	if err != nil {
		// Should never happen for these simple string fields; fall back to a
		// minimal message so stderr is never empty on error.
		return []byte(`{"error":{"code":"` + code + `","message":"failed to render error","exit":` + fmt.Sprintf("%d", exit) + `}}`)
	}
	return append(b, '\n')
}

// errString normalizes an engine error for the engines block: a per-engine
// deadline (context.DeadlineExceeded) is reported as "timeout" per §11; other
// errors use their own message; nil → "".
func errString(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	return err.Error()
}
