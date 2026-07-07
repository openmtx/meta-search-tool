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
	return BuildEnvelope("rust async runtime", runs, results, 10, 6109)
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
	env := BuildEnvelope("q", nil, nil, 10, 5)
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
	env := BuildEnvelope("q", nil, []rank.RankedResult{{Rank: 2, Title: "T", URL: "https://x/y"}}, 10, 1)
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
