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
	engines := make(map[string]EngineStat, len(runs))
	for _, r := range runs {
		engines[r.Engine] = EngineStat{
			OK:     r.Err == nil,
			Count:  len(r.Results),
			TookMs: r.Latency.Milliseconds(),
			Error:  errString(r.Err),
		}
	}
	if results == nil {
		results = []rank.RankedResult{}
	}
	return &Envelope{
		Schema:  SchemaVersion,
		Query:   query,
		TookMs:  tookMs,
		Limit:   limit,
		Engines: engines,
		Results: results,
	}
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
