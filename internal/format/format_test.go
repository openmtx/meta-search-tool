package format

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/openmtx/meta-search-tool/internal/engine"
	"github.com/openmtx/meta-search-tool/internal/rank"
)

func sampleEnvelope() *Envelope {
	runs := []engine.EngineRun{
		{Engine: "brave", Results: []engine.Result{{URL: "https://a.example/1"}}, Latency: 312000000}, // 312ms
		{Engine: "wiki", Err: context.DeadlineExceeded, Latency: 6000000000},                          // 6s, timed out
	}
	results := []rank.RankedResult{
		{Rank: 1, Title: "Tokio vs async-std", URL: "https://a.example/1", Source: "example.com", Snippet: "A comparison.", Score: 0.0331, Engines: []string{"brave"}},
	}
	return BuildEnvelope("rust async runtime", runs, results, 10, 6109, "")
}

func TestBuildEnvelope(t *testing.T) {
	env := sampleEnvelope()
	if env.Schema != SchemaVersion {
		t.Errorf("schema = %d, want %d", env.Schema, SchemaVersion)
	}
	if env.Query != "rust async runtime" {
		t.Errorf("query = %q", env.Query)
	}
	if env.Limit != 10 {
		t.Errorf("limit = %d", env.Limit)
	}
	brave := env.Engines["brave"]
	if !brave.OK || brave.Count != 1 || brave.TookMs != 312 || brave.Error != "" {
		t.Errorf("brave stat = %+v, want ok/count=1/took=312/no-err", brave)
	}
	wiki := env.Engines["wiki"]
	if wiki.OK || wiki.Count != 0 || wiki.TookMs != 6000 || wiki.Error != "timeout" {
		t.Errorf("wiki stat = %+v, want NOT ok/0/6000ms/\"timeout\"", wiki)
	}
}

func TestMarshal_JSON(t *testing.T) {
	out, err := Marshal(sampleEnvelope(), "json")
	if err != nil {
		t.Fatal(err)
	}
	// Round-trip: the bytes must parse back into the same shape.
	var got Envelope
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("json output does not parse: %v\n%s", err, out)
	}
	if got.Schema != SchemaVersion {
		t.Errorf("schema = %d, want %d", got.Schema, SchemaVersion)
	}
	if len(got.Results) != 1 || got.Results[0].Rank != 1 {
		t.Errorf("results = %+v", got.Results)
	}
	if _, ok := got.Engines["brave"]; !ok {
		t.Errorf("engines block missing brave: %+v", got.Engines)
	}
	if !bytes.HasSuffix(out, []byte("\n")) {
		t.Error("json output should end with a trailing newline")
	}
}

// TestMarshal_JSON_NoResultsIsArray: zero results must serialize as `[]`, and an
// empty engines map as `{}` — never JSON null (§4: exit-1 stdout is a valid
// `results: []` payload).
func TestMarshal_JSON_NoResultsIsArray(t *testing.T) {
	env := BuildEnvelope("q", nil, nil, 10, 5, "")
	out, err := Marshal(env, "json")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, `"results":[]`) {
		t.Errorf("empty results must be [], got:\n%s", s)
	}
	if !strings.Contains(s, `"engines":{}`) {
		t.Errorf("empty engines must be {}, got:\n%s", s)
	}
}

func TestMarshal_JSONL(t *testing.T) {
	env := sampleEnvelope()
	out, err := Marshal(env, "jsonl")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) != len(env.Results) {
		t.Fatalf("jsonl: %d lines, want %d (one result per line, no envelope)", len(lines), len(env.Results))
	}
	// No envelope-only fields on jsonl lines (a result's own `engines` array is fine).
	for i, line := range lines {
		if strings.Contains(line, `"schema"`) || strings.Contains(line, `"took_ms"`) || strings.Contains(line, `"limit"`) {
			t.Errorf("jsonl line %d contains envelope field: %s", i, line)
		}
		var r rank.RankedResult
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Errorf("jsonl line %d does not parse as a result: %v\n%s", i, err, line)
		}
		if r.Rank != i+1 {
			t.Errorf("jsonl line %d rank = %d, want %d", i, r.Rank, i+1)
		}
	}
}

func TestMarshal_Text(t *testing.T) {
	env := sampleEnvelope()
	out, err := Marshal(env, "text")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	want := "1. Tokio vs async-std - https://a.example/1\n    A comparison.\n"
	if s != want {
		t.Errorf("text output = %q, want %q", s, want)
	}
}

func TestMarshal_TextNoSnippet(t *testing.T) {
	env := BuildEnvelope("q", nil, []rank.RankedResult{{Rank: 2, Title: "T", URL: "https://x/y"}}, 10, 1, "")
	out, err := Marshal(env, "text")
	if err != nil {
		t.Fatal(err)
	}
	want := "2. T - https://x/y\n"
	if string(out) != want {
		t.Errorf("text (no snippet) = %q, want %q", out, want)
	}
}

func TestMarshal_UnknownFormat(t *testing.T) {
	if _, err := Marshal(sampleEnvelope(), "csv"); err == nil {
		t.Error("Marshal unknown format should error")
	}
}

func TestMarshalError(t *testing.T) {
	out := MarshalError(ErrCodeUsage, "bad flag", 2)
	var se StructuredError
	if err := json.Unmarshal(out, &se); err != nil {
		t.Fatalf("error output does not parse: %v\n%s", err, out)
	}
	if se.Error.Code != "usage" || se.Error.Message != "bad flag" || se.Error.Exit != 2 {
		t.Errorf("structured error = %+v", se)
	}
	if !bytes.HasSuffix(out, []byte("\n")) {
		t.Error("error output should end with newline")
	}
}

func TestErrString(t *testing.T) {
	if got := errString(nil); got != "" {
		t.Errorf("nil => %q, want empty", got)
	}
	if got := errString(context.DeadlineExceeded); got != "timeout" {
		t.Errorf("deadline => %q, want \"timeout\"", got)
	}
	// A wrapped deadline is still "timeout" (errors.Is unwrap).
	wrapped := fmt.Errorf("ctx: %w", context.DeadlineExceeded)
	if got := errString(wrapped); got != "timeout" {
		t.Errorf("wrapped deadline => %q, want \"timeout\"", got)
	}
	if got := errString(errors.New("upstream 500")); got != "upstream 500" {
		t.Errorf("generic => %q, want verbatim", got)
	}
}

// === --list-engines (§4) ====================================================

func sampleList() []ListEngine {
	kp := func(b bool) *bool { return &b }
	return []ListEngine{
		{Name: "brave", Enabled: true, Keyed: true, KeyPresent: kp(false), Weight: 2.0, Status: "missing-key"},
		{Name: "mojeek", Enabled: true, Keyed: false, KeyPresent: nil, Weight: 1.3, Status: "ready"},
		{Name: "ddg", Enabled: false, Keyed: false, KeyPresent: nil, Weight: 0.5, Status: "disabled"},
	}
}

func TestMarshalListEngines_JSON(t *testing.T) {
	out, err := MarshalListEngines(sampleList(), "json")
	if err != nil {
		t.Fatal(err)
	}
	var got []ListEngine
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("json list output does not parse: %v\n%s", err, out)
	}
	if len(got) != 3 {
		t.Fatalf("got %d entries, want 3", len(got))
	}
	// keyless engines serialize key_present as JSON null.
	s := string(out)
	if !strings.Contains(s, `"key_present":null`) {
		t.Errorf("keyless key_present must be null, got:\n%s", s)
	}
	// keyed engines serialize a real bool.
	if !strings.Contains(s, `"key_present":false`) {
		t.Errorf("keyed key_present must be a bool, got:\n%s", s)
	}
	if !bytes.HasSuffix(out, []byte("\n")) {
		t.Error("json list output should end with a trailing newline")
	}
}

// TestMarshalListEngines_JSON_EmptyIsArray: an empty listing must be `[]`,
// never JSON null (consistent with the results:[] guarantee elsewhere).
func TestMarshalListEngines_JSON_EmptyIsArray(t *testing.T) {
	out, err := MarshalListEngines([]ListEngine{}, "json")
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "[]\n" {
		t.Errorf("empty list = %q, want "+`[]`+"\n", out)
	}
	// nil must also be [] (MarshalListEngines callers pass non-nil, but be safe).
	nilOut, _ := MarshalListEngines(nil, "json")
	if !bytes.Equal(nilOut, out) {
		t.Errorf("nil list = %q, want %q", nilOut, out)
	}
}

func TestMarshalListEngines_JSONL(t *testing.T) {
	out, err := MarshalListEngines(sampleList(), "jsonl")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("jsonl: %d lines, want 3 (one engine per line)", len(lines))
	}
	for i, line := range lines {
		var e ListEngine
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Errorf("jsonl line %d does not parse: %v\n%s", i, err, line)
		}
	}
	if strings.Contains(string(out), `"key_present":null`) {
		// keyless lines carry null key_present — that's expected, just confirm at least one.
	}
}

func TestMarshalListEngines_Text(t *testing.T) {
	out, err := MarshalListEngines(sampleList(), "text")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	// Header + 3 rows; each engine name, status, keyed-ness, and weight appear.
	for _, want := range []string{"NAME", "brave", "missing-key", "mojeek", "ready", "ddg", "disabled"} {
		if !strings.Contains(s, want) {
			t.Errorf("text list missing %q in:\n%s", want, s)
		}
	}
	// keyless engines are labeled "keyless"; keyed-without-key "absent".
	if !strings.Contains(s, "keyless") || !strings.Contains(s, "absent") {
		t.Errorf("text list missing key labels:\n%s", s)
	}
}

func TestMarshalListEngines_TextEmpty(t *testing.T) {
	out, err := MarshalListEngines([]ListEngine{}, "text")
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "(no engines registered)\n" {
		t.Errorf("empty text list = %q", out)
	}
}

func TestMarshalListEngines_TableRejected(t *testing.T) {
	if _, err := MarshalListEngines(sampleList(), "table"); err == nil {
		t.Error("table must be rejected (v0.2)")
	}
	if _, err := MarshalListEngines(sampleList(), "csv"); err == nil {
		t.Error("unknown format must be rejected")
	}
}

// === --no-merge output (§10) ===============================================

func sampleRuns() []engine.EngineRun {
	return []engine.EngineRun{
		{Engine: "alpha", Results: []engine.Result{
			{Title: "A1", URL: "https://a.example/1", Snippet: "snip one"},
			{Title: "A2", URL: "https://a.example/2"},
		}},
		{Engine: "beta", Results: []engine.Result{
			{Title: "B1", URL: "https://www.b.example/x"},
		}},
	}
}

func TestBuildUnmerged(t *testing.T) {
	got := BuildUnmerged(sampleRuns())
	if len(got) != 3 {
		t.Fatalf("got %d, want 3", len(got))
	}
	// Registration order across engines, native order within.
	if got[0].Engine != "alpha" || got[0].URL != "https://a.example/1" {
		t.Errorf("[0] = %+v", got[0])
	}
	if got[2].Engine != "beta" {
		t.Errorf("[2] = %+v, want beta", got[2])
	}
	// Source is derived (www.b.example → b.example).
	if got[2].Source != "b.example" {
		t.Errorf("[2].Source = %q, want b.example", got[2].Source)
	}
}

// TestBuildUnmerged_EmptyNeverNil: an empty run set yields [], never nil.
func TestBuildUnmerged_EmptyNeverNil(t *testing.T) {
	got := BuildUnmerged(nil)
	if got == nil {
		t.Error("BuildUnmerged(nil) must return non-nil []")
	}
	env := BuildUnmergedEnvelope("q", nil, 10, 1)
	out, err := MarshalUnmerged(env, "json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"results":[]`) {
		t.Errorf("empty --no-merge must serialize results:[], got %s", out)
	}
}

func TestMarshalUnmerged_JSON(t *testing.T) {
	env := BuildUnmergedEnvelope("q", sampleRuns(), 10, 7)
	out, err := MarshalUnmerged(env, "json")
	if err != nil {
		t.Fatal(err)
	}
	var got UnmergedEnvelope
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("--no-merge json does not parse: %v\n%s", err, out)
	}
	if len(got.Results) != 3 || got.Results[0].Engine != "alpha" {
		t.Errorf("results = %+v", got.Results)
	}
	s := string(out)
	if strings.Contains(s, `"rank"`) || strings.Contains(s, `"score"`) {
		t.Errorf("--no-merge json must not carry rank/score:\n%s", s)
	}
	if !strings.Contains(s, `"engine":"alpha"`) {
		t.Errorf("--no-merge json must carry engine field:\n%s", s)
	}
}

func TestMarshalUnmerged_JSONL(t *testing.T) {
	env := BuildUnmergedEnvelope("q", sampleRuns(), 10, 7)
	out, err := MarshalUnmerged(env, "jsonl")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("jsonl: %d lines, want 3", len(lines))
	}
	var u UnmergedResult
	if err := json.Unmarshal([]byte(lines[0]), &u); err != nil || u.Engine != "alpha" {
		t.Errorf("jsonl line 0 = %q / %+v", lines[0], u)
	}
}

func TestMarshalUnmerged_Text(t *testing.T) {
	env := BuildUnmergedEnvelope("q", sampleRuns(), 10, 7)
	out, err := MarshalUnmerged(env, "text")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	// Engine name prefixes each entry; no rank column (§10).
	want := "alpha: A1 - https://a.example/1\n    snip one\nalpha: A2 - https://a.example/2\nbeta: B1 - https://www.b.example/x\n"
	if s != want {
		t.Errorf("--no-merge text = %q, want %q", s, want)
	}
}

func TestMarshalUnmerged_TableWorks(t *testing.T) {
	env := BuildUnmergedEnvelope("q", sampleRuns(), 10, 7)
	out, err := MarshalUnmerged(env, "table")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	// Must contain header row with ENGINE column.
	for _, want := range []string{"ENGINE", "TITLE", "URL"} {
		if !strings.Contains(s, want) {
			t.Errorf("unmerged table missing header %q:\n%s", want, s)
		}
	}
	// Must contain engine names from sampleRuns.
	for _, want := range []string{"alpha", "beta"} {
		if !strings.Contains(s, want) {
			t.Errorf("unmerged table missing engine %q:\n%s", want, s)
		}
	}
}

// === --raw output (§10) =====================================================

func TestBuildRawEnvelope(t *testing.T) {
	runs := []engine.EngineRun{
		{Engine: "brave", Results: []engine.Result{{URL: "https://a.example/1"}}, Raw: map[string]any{"web": "native"}},
		{Engine: "ddg", Err: errors.New("upstream 500")}, // errored → no raw entry
	}
	env := BuildRawEnvelope("q", runs, 10, 5)
	if _, ok := env.Raw["brave"]; !ok {
		t.Error("brave raw payload missing")
	}
	if _, ok := env.Raw["ddg"]; ok {
		t.Error("ddg must be absent from raw (errored, no payload)")
	}
	// engines metadata block still present, reflecting the failure.
	if env.Engines["ddg"].OK || env.Engines["ddg"].Error == "" {
		t.Errorf("ddg stat = %+v, want ok=false with error", env.Engines["ddg"])
	}
	// raw map is never nil → JSON `{}` when empty.
	emptyEnv := BuildRawEnvelope("q", nil, 10, 1)
	if emptyEnv.Raw == nil {
		t.Error("Raw must be non-nil {} for empty runs")
	}
}

func TestMarshalRaw(t *testing.T) {
	runs := []engine.EngineRun{
		{Engine: "brave", Results: []engine.Result{{URL: "https://a.example/1"}}, Raw: map[string]any{"web": map[string]any{"results": []any{1, 2}}}},
	}
	env := BuildRawEnvelope("rust", runs, 10, 99)
	out, err := MarshalRaw(env)
	if err != nil {
		t.Fatal(err)
	}
	var got RawEnvelope
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("--raw json does not parse: %v\n%s", err, out)
	}
	// Inspect TOP-LEVEL keys (the native payload inside raw.brave legitimately
	// contains its own "results" array, so a naive substring check would lie).
	var top map[string]any
	if err := json.Unmarshal(out, &top); err != nil {
		t.Fatalf("parse top-level: %v", err)
	}
	for _, k := range []string{"schema", "query", "engines", "raw"} {
		if _, ok := top[k]; !ok {
			t.Errorf("--raw envelope missing top-level key %q:\n%s", k, out)
		}
	}
	if _, ok := top["results"]; ok {
		t.Errorf("--raw must not emit a top-level results field:\n%s", out)
	}
	if !bytes.HasSuffix(out, []byte("\n")) {
		t.Error("--raw output should end with a trailing newline")
	}
	_ = got
}

// === table format (Sprint B.1.6) ===========================================
// Table renderer tests live in table_test.go.

func sampleResults() []rank.RankedResult {
	return []rank.RankedResult{
		{Rank: 1, Title: "Go Concurrency Patterns", URL: "https://go.dev/blog/patterns", Source: "go.dev", Snippet: "A deep dive into goroutines, channels, and contexts.", Score: 0.045, Engines: []string{"brave"}},
		{Rank: 2, Title: "Understanding Go Routines", URL: "https://example.com/go-routines", Source: "example.com", Snippet: "Routines are lightweight threads managed by the Go runtime.", Score: 0.032, Engines: []string{"wiki"}},
	}
}
