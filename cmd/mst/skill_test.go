package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/openmtx/meta-search-tool/internal/cgoapi"
	"github.com/openmtx/meta-search-tool/internal/format"
	"github.com/openmtx/meta-search-tool/internal/rank"
)

// === --schema / --help-json tests ==========================================

func TestRun_Schema(t *testing.T) {
	reg := newReg()
	code, out, errs := runWith(t, []string{"--schema", "query"}, reg, "")
	if code != 0 {
		t.Fatalf("exit = %d; stderr=%s", code, errs)
	}
	var schema schemaOutput
	if err := json.Unmarshal([]byte(out), &schema); err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	if schema.Version != 1 {
		t.Errorf("version = %d, want 1", schema.Version)
	}
	if schema.Tool != "mst" {
		t.Errorf("tool = %q, want mst", schema.Tool)
	}
	if len(schema.Flags) == 0 {
		t.Fatal("no flags in schema")
	}
	flagNames := make(map[string]bool)
	for _, f := range schema.Flags {
		flagNames[f.Name] = true
	}
	for _, want := range []string{"query", "engine", "limit", "format", "fields", "schema", "dry-run"} {
		if !flagNames[want] {
			t.Errorf("missing flag %q in schema", want)
		}
	}
}

func TestRun_HelpJSON(t *testing.T) {
	reg := newReg()
	code, out1, _ := runWith(t, []string{"--schema", "query"}, reg, "")
	code2, out2, _ := runWith(t, []string{"--help-json", "query"}, reg, "")
	if code != 0 || code2 != 0 {
		t.Fatalf("exit = %d, %d", code, code2)
	}
	if out1 != out2 {
		t.Errorf("--schema and --help-json produce different output")
	}
}

// === --dry-run tests ========================================================

func TestRun_DryRun(t *testing.T) {
	reg := newReg(
		&fakeEngine{name: "alpha", weight: 1.0, enabled: true},
		&fakeEngine{name: "beta", weight: 2.0, enabled: true, keyed: true, keyEnv: "BETA_KEY"},
	)
	code, out, errs := runWith(t, []string{"--dry-run", "-e", "alpha,beta", "--weight", "alpha=1.5", "test query"}, reg, "")
	if code != 0 {
		t.Fatalf("exit = %d; stderr=%s", code, errs)
	}
	var dry cgoapi.DryRunOutput
	if err := json.Unmarshal([]byte(out), &dry); err != nil {
		t.Fatalf("parse dry-run: %v", err)
	}
	if dry.Query != "test query" {
		t.Errorf("query = %q, want test query", dry.Query)
	}
	if dry.Limit != 10 {
		t.Errorf("limit = %d, want 10", dry.Limit)
	}
	if dry.Engines == nil || len(dry.Engines) != 2 {
		t.Fatalf("engines = %v, want 2 engines", dry.Engines)
	}
	for _, e := range dry.Engines {
		if e.Name == "alpha" && e.Weight != 1.5 {
			t.Errorf("alpha weight = %g, want 1.5", e.Weight)
		}
		if e.Name == "beta" && e.Status != "missing-key" {
			t.Errorf("beta status = %q, want missing-key", e.Status)
		}
	}
}

func TestRun_DryRun_UnknownEngine(t *testing.T) {
	reg := newReg()
	code, _, errs := runWith(t, []string{"--dry-run", "-e", "bogus", "q"}, reg, "")
	if code != 2 {
		t.Fatalf("exit = %d, want 2; stderr=%s", code, errs)
	}
}

func TestRun_DryRun_NoQuery(t *testing.T) {
	reg := newReg()
	code, _, errs := runWith(t, []string{"--dry-run"}, reg, "")
	if code != 2 {
		t.Fatalf("exit = %d, want 2; stderr=%s", code, errs)
	}
}

// === --fields tests =========================================================

func TestRun_Fields_JSON(t *testing.T) {
	url1 := "https://example.com/1"
	url2 := "https://example.com/2"
	reg := newReg(
		eng("alpha", r("A1", url1), r("A2", url2)),
	)
	code, out, errs := runWith(t, []string{"--fields", "url", "--fields", "title", "-n", "5", "query"}, reg, "")
	if code != 0 {
		t.Fatalf("exit = %d; stderr=%s", code, errs)
	}
	var env format.Envelope
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(env.Results) != 2 {
		t.Fatalf("results = %d, want 2", len(env.Results))
	}
	for i, res := range env.Results {
		// After unmarshaling JSON into format.Envelope, zero values are set
		// for missing keys. Check that non-requested fields are zero/empty.
		if res.Rank != 0 {
			t.Errorf("result %d: unexpected rank=%d", i, res.Rank)
		}
		if res.Score != 0 {
			t.Errorf("result %d: unexpected score=%.4f", i, res.Score)
		}
		if res.Source != "" {
			t.Errorf("result %d: unexpected source=%q", i, res.Source)
		}
		if len(res.Engines) != 0 {
			t.Errorf("result %d: unexpected engines=%v", i, res.Engines)
		}
		// Requested fields must be populated.
		if res.Title == "" {
			t.Errorf("result %d: missing title", i)
		}
		if res.URL == "" {
			t.Errorf("result %d: missing url", i)
		}
	}
}

func TestRun_Fields_Text(t *testing.T) {
	reg := newReg(
		eng("alpha", r("A1", "https://example.com/1")),
	)
	code, out, errs := runWith(t, []string{"--fields", "title", "-f", "text", "query"}, reg, "")
	if code != 0 {
		t.Fatalf("exit = %d; stderr=%s", code, errs)
	}
	if !strings.Contains(out, "A1") {
		t.Errorf("text output missing title: %q", out)
	}
}

func TestRun_Fields_InvalidField(t *testing.T) {
	reg := newReg(eng("alpha", r("A1", "https://example.com/1")))
	code, _, errs := runWith(t, []string{"--fields", "bogus_field", "query"}, reg, "")
	if code != 2 {
		t.Fatalf("exit = %d, want 2; stderr=%s", code, errs)
	}
}

func TestRun_Fields_DuplicateField(t *testing.T) {
	reg := newReg(eng("alpha", r("A1", "https://example.com/1")))
	code, _, errs := runWith(t, []string{"--fields", "url", "--fields", "url", "query"}, reg, "")
	if code != 2 {
		t.Fatalf("exit = %d, want 2; stderr=%s", code, errs)
	}
}

func TestRun_Fields_Empty(t *testing.T) {
	url1 := "https://example.com/1"
	reg := newReg(
		eng("alpha", r("A1", url1)),
	)
	code, out, errs := runWith(t, []string{"-n", "5", "query"}, reg, "")
	if code != 0 {
		t.Fatalf("exit = %d; stderr=%s", code, errs)
	}
	var env format.Envelope
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(env.Results) != 1 {
		t.Fatalf("results = %d, want 1", len(env.Results))
	}
	b, _ := json.Marshal(env.Results[0])
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, key := range []string{"rank", "title", "url", "source", "score", "engines"} {
		if _, ok := m[key]; !ok {
			t.Errorf("missing expected field %q in full output", key)
		}
	}
}

// === validateFields / validField unit tests =================================

func TestValidateFields(t *testing.T) {
	tests := []struct {
		name    string
		fields  []string
		wantErr bool
	}{
		{"empty", nil, false},
		{"empty slice", []string{}, false},
		{"valid single", []string{"url"}, false},
		{"valid multiple", []string{"url", "title", "snippet"}, false},
		{"case insensitive", []string{"URL", "Title"}, false},
		{"invalid field", []string{"bogus"}, true},
		{"mixed valid/invalid", []string{"url", "bogus"}, true},
		{"duplicate", []string{"url", "url"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateFields(tt.fields)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateFields(%v) err=%v, wantErr=%v", tt.fields, err, tt.wantErr)
			}
		})
	}
}

func TestValidField(t *testing.T) {
	valid := []string{"rank", "title", "url", "source", "snippet", "score", "engines"}
	invalid := []string{"bogus", "id", "name", ""}
	for _, f := range valid {
		if !validField(f) {
			t.Errorf("validField(%q) = false, want true", f)
		}
	}
	for _, f := range invalid {
		if validField(f) {
			t.Errorf("validField(%q) = true, want false", f)
		}
	}
}

func TestProjectResults(t *testing.T) {
	results := []rank.RankedResult{
		{Title: "T1", URL: "https://example.com/1", Snippet: "S1"},
		{Title: "T2", URL: "https://example.com/2", Snippet: "S2"},
	}
	projected := projectResults(results, []string{"title", "url"})
	if len(projected) != 2 {
		t.Fatalf("projected = %d results, want 2", len(projected))
	}
	for i, p := range projected {
		if _, ok := p["rank"]; ok {
			t.Errorf("result %d: unexpected rank field", i)
		}
		if _, ok := p["title"]; !ok {
			t.Errorf("result %d: missing title field", i)
		}
		if _, ok := p["url"]; !ok {
			t.Errorf("result %d: missing url field", i)
		}
	}
}

func TestMarshalResults_NoProjection(t *testing.T) {
	out, err := MarshalResults(nil, "json", nil, "q", 100, 10, nil, "")
	if err != nil {
		t.Fatalf("MarshalResults: %v", err)
	}
	var env format.Envelope
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if env.Query != "q" {
		t.Errorf("query = %q, want q", env.Query)
	}
}

func TestMarshalResults_JsonlProjection(t *testing.T) {
	results := []rank.RankedResult{
		{Title: "T1", URL: "https://example.com/1"},
		{Title: "T2", URL: "https://example.com/2"},
	}
	out, err := MarshalResults(results, "jsonl", []string{"url"}, "q", 100, 2, nil, "")
	if err != nil {
		t.Fatalf("MarshalResults: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 2 {
		t.Fatalf("jsonl lines = %d, want 2", len(lines))
	}
	for _, line := range lines {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("parse line %q: %v", line, err)
		}
		if _, ok := m["title"]; ok {
			t.Errorf("line has unexpected title field")
		}
		if _, ok := m["url"]; !ok {
			t.Error("line missing url field")
		}
	}
}

// === projectResults: full result round-trip (engine.Result → RankedResult) ==

func TestProjectResults_RoundTrip(t *testing.T) {
	full := []rank.RankedResult{
		{Title: "T1", URL: "https://example.com/1", Snippet: "S1", Score: 0.95, Engines: []string{"alpha"}},
		{Title: "T2", URL: "https://example.com/2", Snippet: "S2", Score: 0.85, Engines: []string{"beta"}},
	}
	projected := projectResults(full, []string{"title", "url"})
	if len(projected) != 2 {
		t.Fatalf("projected = %d, want 2", len(projected))
	}
	for i, p := range projected {
		if _, ok := p["rank"]; ok {
			t.Errorf("result %d: unexpected rank", i)
		}
		if _, ok := p["score"]; ok {
			t.Errorf("result %d: unexpected score", i)
		}
		if _, ok := p["engines"]; ok {
			t.Errorf("result %d: unexpected engines", i)
		}
		if _, ok := p["source"]; ok {
			t.Errorf("result %d: unexpected source", i)
		}
	}
}

// === MarshalResults: all format + field combos ==============================

func TestMarshalResults_AllFormats(t *testing.T) {
	results := []rank.RankedResult{
		{Title: "T1", URL: "https://example.com/1"},
	}
	formats := []string{"json", "jsonl", "text", "table"}
	for _, fmt := range formats {
		out, err := MarshalResults(results, fmt, nil, "q", 100, 1, nil, "")
		if err != nil {
			t.Errorf("format %s: %v", fmt, err)
		}
		if len(out) == 0 {
			t.Errorf("format %s produced empty output", fmt)
		}
	}
}

func TestMarshalResults_InvalidFormat(t *testing.T) {
	_, err := MarshalResults(nil, "xml", nil, "q", 100, 1, nil, "")
	if err == nil {
		t.Error("expected error for invalid format")
	}
}
