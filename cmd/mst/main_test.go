package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/openmtx/meta-search-tool/internal/engine"
	"github.com/openmtx/meta-search-tool/internal/engine/brave"
	"github.com/openmtx/meta-search-tool/internal/engine/ddg"
	"github.com/openmtx/meta-search-tool/internal/engine/wikipedia"
	"github.com/openmtx/meta-search-tool/internal/format"
	"github.com/openmtx/meta-search-tool/internal/rank"
)

// fakeEngine is a configurable engine for end-to-end run() tests.
type fakeEngine struct {
	name    string
	weight  float64
	keyed   bool
	keyEnv  string
	enabled bool
	results []engine.Result
	err     error
	delay   time.Duration
}

func (f *fakeEngine) Name() string { return f.name }
func (f *fakeEngine) Descriptor() engine.EngineInfo {
	return engine.EngineInfo{
		Name: f.name, Keyed: f.keyed, KeyEnv: f.keyEnv,
		DefaultWeight: f.weight, Enabled: f.enabled,
	}
}
func (f *fakeEngine) Search(ctx context.Context, q string, opts engine.Options) ([]engine.Result, error) {
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return f.results, f.err
}

// newReg builds a registry from the given engines (in order).
func newReg(engines ...engine.Engine) *engine.Registry {
	r := engine.NewRegistry()
	for _, e := range engines {
		if err := r.Register(e); err != nil {
			panic(err)
		}
	}
	return r
}

// eng is a shorthand fake builder.
func eng(name string, results ...engine.Result) *fakeEngine {
	return &fakeEngine{name: name, weight: 1.0, enabled: true, results: results}
}

// runWith isolates config (no config file → defaults), runs, and returns
// (exitCode, stdout, stderr).
func runWith(t *testing.T, args []string, reg *engine.Registry, stdin string) (int, string, string) {
	t.Helper()
	t.Setenv("MST_CONFIG_HOME", t.TempDir())
	var out, errs bytes.Buffer
	code := run(args, strings.NewReader(stdin), &out, &errs, reg)
	return code, out.String(), errs.String()
}

func r(title, url string) engine.Result {
	return engine.Result{Title: title, URL: url, Snippet: "snip " + url}
}

// === End-to-end: fake engines → correctly ranked JSON (DoD) =================

func TestRun_E2E_RankedJSON(t *testing.T) {
	url1 := "https://example.com/shared"
	url2 := "https://example.com/only-alpha"
	reg := newReg(
		eng("alpha", r("A1", url1), r("A2", url2)), // url1@r0, url2@r1
		eng("beta", r("B1", url1)),                 // url1@r0
	)
	code, out, errs := runWith(t, []string{"-n", "10", "query"}, reg, "")
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, errs)
	}
	var env format.Envelope
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("stdout not valid JSON: %v\n%s", err, out)
	}
	if env.Schema != 1 || env.Query != "query" {
		t.Errorf("envelope schema/query = %d/%q", env.Schema, env.Query)
	}
	if len(env.Results) != 2 {
		t.Fatalf("results = %d, want 2", len(env.Results))
	}
	// url1 is fused from both engines (score 2/60) and must lead; url2 only alpha.
	if env.Results[0].URL != url1 {
		t.Errorf("top url = %q, want %q", env.Results[0].URL, url1)
	}
	if !sliceEq(env.Results[0].Engines, []string{"alpha", "beta"}) {
		t.Errorf("top engines = %v, want [alpha beta] (equal contrib → reg order)", env.Results[0].Engines)
	}
	wantScore := 1.0/60.0 + 1.0/60.0
	if !floatEq(env.Results[0].Score, wantScore) {
		t.Errorf("top score = %v, want %v", env.Results[0].Score, wantScore)
	}
	if env.Results[0].Rank != 1 || env.Results[1].Rank != 2 {
		t.Errorf("ranks = %d,%d, want 1,2", env.Results[0].Rank, env.Results[1].Rank)
	}
	if env.Results[1].URL != url2 {
		t.Errorf("2nd url = %q, want %q", env.Results[1].URL, url2)
	}
	// engines block carries per-engine ok/count.
	if env.Engines["alpha"].Count != 2 || !env.Engines["alpha"].OK {
		t.Errorf("alpha stat = %+v, want ok/count=2", env.Engines["alpha"])
	}
	if env.Engines["beta"].Count != 1 || !env.Engines["beta"].OK {
		t.Errorf("beta stat = %+v, want ok/count=1", env.Engines["beta"])
	}
}

// === Exit codes (DoD: 0/1/2/3 per §4) =======================================

func TestRun_Exit0_WithResults(t *testing.T) {
	reg := newReg(eng("a", r("t", "https://x/1")))
	code, out, _ := runWith(t, []string{"q"}, reg, "")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, out)
	}
}

func TestRun_Exit1_NoResults(t *testing.T) {
	reg := newReg(eng("a")) // returns nothing
	code, out, errs := runWith(t, []string{"q"}, reg, "")
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(out, `"results":[]`) {
		t.Errorf("exit-1 stdout must be a valid results:[] payload, got %s", out)
	}
	assertStructuredError(t, errs, format.ErrCodeNoResults, 1)
}

func TestRun_Exit1_NoReadyEngines(t *testing.T) {
	// A keyed engine with no key → not ready → nothing dispatched.
	reg := newReg(&fakeEngine{name: "brave", weight: 2.0, enabled: true, keyed: true, keyEnv: "BRAVE_API_KEY"})
	code, out, errs := runWith(t, []string{"q"}, reg, "")
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(errs, "no ready engines") {
		t.Errorf("exit-1 message should hint at no ready engines, got %s", errs)
	}
	_ = out
}

func TestRun_Exit2_BadFlag(t *testing.T) {
	reg := newReg(eng("a", r("t", "https://x/1")))
	code, out, errs := runWith(t, []string{"--bogus", "q"}, reg, "")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if out != "" {
		t.Errorf("exit-2 must write NOTHING to stdout, got %q", out)
	}
	assertStructuredError(t, errs, format.ErrCodeUsage, 2)
}

func TestRun_Exit2_UnknownEngine(t *testing.T) {
	reg := newReg(eng("a", r("t", "https://x/1")))
	code, out, errs := runWith(t, []string{"--engine", "bogus", "q"}, reg, "")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if out != "" {
		t.Errorf("exit-2 stdout must be empty, got %q", out)
	}
	if !strings.Contains(errs, "unknown engine") || !strings.Contains(errs, "valid:") {
		t.Errorf("unknown-engine error should list valid names, got %s", errs)
	}
}

func TestRun_Exit2_NoQuery(t *testing.T) {
	reg := newReg(eng("a", r("t", "https://x/1")))
	code, _, errs := runWith(t, nil, reg, "")
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (no query)", code)
	}
	assertStructuredError(t, errs, format.ErrCodeUsage, 2)
}

func TestRun_Exit2_EmptyPositionalQuery(t *testing.T) {
	reg := newReg(eng("a", r("t", "https://x/1")))
	code, _, _ := runWith(t, []string{"   "}, reg, "")
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (whitespace-only query)", code)
	}
}

func TestRun_Exit2_RequireMissingKey(t *testing.T) {
	reg := newReg(&fakeEngine{name: "brave", weight: 2.0, enabled: true, keyed: true, keyEnv: "BRAVE_API_KEY"})
	code, out, errs := runWith(t, []string{"--require", "brave", "q"}, reg, "")
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (require'd engine cannot start)", code)
	}
	if out != "" {
		t.Errorf("exit-2 stdout must be empty, got %q", out)
	}
	if !strings.Contains(errs, "cannot start") {
		t.Errorf("require-cannot-start message missing, got %s", errs)
	}
}

func TestRun_Exit2_RequireExcludeConflict(t *testing.T) {
	reg := newReg(eng("a", r("t", "https://x/1")))
	code, _, errs := runWith(t, []string{"--require", "a", "--exclude", "a", "q"}, reg, "")
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (require∩exclude)", code)
	}
	if !strings.Contains(errs, "both --require and --exclude") {
		t.Errorf("conflict message missing, got %s", errs)
	}
}

func TestRun_Exit3_RequiredEngineFailed(t *testing.T) {
	boom := errors.New("upstream 500")
	reg := newReg(
		eng("ok", r("t", "https://x/1")),
		&fakeEngine{name: "flaky", weight: 1.0, enabled: true, err: boom},
	)
	code, out, errs := runWith(t, []string{"--require", "flaky", "q"}, reg, "")
	if code != 3 {
		t.Fatalf("exit = %d, want 3 (required ran but failed)", code)
	}
	// Exit 3 still emits the full payload on stdout.
	if !strings.Contains(out, "https://x/1") {
		t.Errorf("exit-3 stdout should carry results, got %s", out)
	}
	assertStructuredError(t, errs, format.ErrCodeEngineFailure, 3)
}

func TestRun_Exit3_Strict(t *testing.T) {
	boom := errors.New("upstream 500")
	reg := newReg(
		eng("ok", r("t", "https://x/1")),
		&fakeEngine{name: "flaky", weight: 1.0, enabled: true, err: boom},
	)
	// Without --strict this is exit 0 (results present, non-required failure).
	code0, _, _ := runWith(t, []string{"q"}, reg, "")
	if code0 != 0 {
		t.Fatalf("baseline (no --strict) exit = %d, want 0", code0)
	}
	// With --strict it is exit 3.
	code3, out, errs := runWith(t, []string{"--strict", "q"}, reg, "")
	if code3 != 3 {
		t.Fatalf("--strict exit = %d, want 3", code3)
	}
	if !strings.Contains(out, "https://x/1") {
		t.Errorf("exit-3 stdout should carry results, got %s", out)
	}
	assertStructuredError(t, errs, format.ErrCodeEngineFailure, 3)
}

func TestRun_Exit3_BeatsExit1(t *testing.T) {
	// Required engine fails AND there are no results → exit 3, not 1 (§4).
	boom := errors.New("upstream 500")
	reg := newReg(&fakeEngine{name: "only", weight: 1.0, enabled: true, err: boom})
	code, out, errs := runWith(t, []string{"--require", "only", "q"}, reg, "")
	if code != 3 {
		t.Fatalf("exit = %d, want 3 (exit 3 beats exit 1)", code)
	}
	assertStructuredError(t, errs, format.ErrCodeEngineFailure, 3)
	if !strings.Contains(out, `"results":[]`) {
		t.Errorf("zero-result exit-3 stdout should still be results:[], got %s", out)
	}
}

// === Formats & query sources ================================================

func TestRun_JSONL(t *testing.T) {
	reg := newReg(eng("a", r("A", "https://x/1"), r("B", "https://x/2")))
	_, out, _ := runWith(t, []string{"-f", "jsonl", "q"}, reg, "")
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("jsonl: %d lines, want 2", len(lines))
	}
	for _, l := range lines {
		var r rank.RankedResult
		if err := json.Unmarshal([]byte(l), &r); err != nil {
			t.Errorf("jsonl line not a result: %v: %s", err, l)
		}
	}
}

func TestRun_Text(t *testing.T) {
	reg := newReg(eng("a", r("Hello", "https://x/1")))
	_, out, _ := runWith(t, []string{"-f", "text", "q"}, reg, "")
	want := "1. Hello - https://x/1\n    snip https://x/1\n"
	if out != want {
		t.Errorf("text output = %q, want %q", out, want)
	}
}

func TestRun_StdinQuery(t *testing.T) {
	reg := newReg(eng("a", r("t", "https://x/1")))
	code, out, _ := runWith(t, []string{"--stdin"}, reg, "from stdin\n")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(out, `"query":"from stdin"`) {
		t.Errorf("stdin query not reflected: %s", out)
	}
}

func TestRun_VersionAndHelp(t *testing.T) {
	reg := newReg()
	if code, out, _ := runWith(t, []string{"--version"}, reg, ""); code != 0 || !strings.HasPrefix(out, "mst ") {
		t.Errorf("--version: code=%d out=%q", code, out)
	}
	if code, _, _ := runWith(t, []string{"--help"}, reg, ""); code != 0 {
		t.Errorf("--help: code=%d, want 0", code)
	}
}

func TestRun_RawNoMergeTableDeferred(t *testing.T) {
	reg := newReg(eng("a", r("t", "https://x/1")))
	for _, args := range [][]string{
		{"--raw", "q"},
		{"--no-merge", "q"},
		{"-f", "table", "q"},
	} {
		code, out, errs := runWith(t, args, reg, "")
		if code != 2 {
			t.Errorf("%v: exit=%d, want 2 (deferred mode must surface, not no-op)", args, code)
		}
		if out != "" {
			t.Errorf("%v: stdout must be empty on exit 2, got %q", args, out)
		}
		if !strings.Contains(errs, "not yet implemented") {
			t.Errorf("%v: expected 'not yet implemented' message, got %s", args, errs)
		}
	}
}

// === --list-engines (§4) ====================================================

// listReg builds a registry that exercises every status: brave (keyed, no key →
// missing-key), mojeek (keyless → ready), ddg (disabled by default).
func listReg() *engine.Registry {
	return newReg(
		&fakeEngine{name: "brave", weight: 2.0, enabled: true, keyed: true, keyEnv: "BRAVE_API_KEY"},
		&fakeEngine{name: "mojeek", weight: 1.3, enabled: true},
		&fakeEngine{name: "ddg", weight: 0.5, enabled: false},
	)
}

func findEntry(t *testing.T, entries []format.ListEngine, name string) format.ListEngine {
	t.Helper()
	for _, e := range entries {
		if e.Name == name {
			return e
		}
	}
	t.Fatalf("no --list-engines entry named %q in %+v", name, entries)
	return format.ListEngine{}
}

func TestRun_ListEngines_JSON(t *testing.T) {
	code, out, errs := runWith(t, []string{"--list-engines"}, listReg(), "")
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, errs)
	}
	var entries []format.ListEngine
	if err := json.Unmarshal([]byte(out), &entries); err != nil {
		t.Fatalf("--list-engines stdout not a JSON array: %v\n%s", err, out)
	}
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(entries))
	}
	brave := findEntry(t, entries, "brave")
	if !brave.Keyed || brave.KeyPresent == nil || *brave.KeyPresent {
		t.Errorf("brave = %+v, want keyed/key_present=false", brave)
	}
	if brave.Status != "missing-key" || brave.Weight != 2.0 || !brave.Enabled {
		t.Errorf("brave = %+v, want enabled/weight=2/missing-key", brave)
	}
	mojeek := findEntry(t, entries, "mojeek")
	if mojeek.Keyed || mojeek.KeyPresent != nil {
		t.Errorf("mojeek = %+v, want keyless (key_present=null)", mojeek)
	}
	if mojeek.Status != "ready" || mojeek.Weight != 1.3 {
		t.Errorf("mojeek = %+v, want ready/weight=1.3", mojeek)
	}
	ddg := findEntry(t, entries, "ddg")
	if ddg.Status != "disabled" || ddg.Enabled {
		t.Errorf("ddg = %+v, want disabled", ddg)
	}
	// keyless key_present serializes as JSON null.
	if !strings.Contains(out, `"key_present":null`) {
		t.Errorf("keyless key_present must be null, got %s", out)
	}
}

// TestRun_ListEngines_KeyPresent: with the key env var set, a keyed engine's
// status becomes ready and key_present becomes true.
func TestRun_ListEngines_KeyPresent(t *testing.T) {
	t.Setenv("BRAVE_API_KEY", "test-key")
	code, out, _ := runWith(t, []string{"--list-engines"}, listReg(), "")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	var entries []format.ListEngine
	json.Unmarshal([]byte(out), &entries)
	brave := findEntry(t, entries, "brave")
	if brave.Status != "ready" || brave.KeyPresent == nil || !*brave.KeyPresent {
		t.Errorf("brave with key = %+v, want ready/key_present=true", brave)
	}
}

func TestRun_ListEngines_JSONL(t *testing.T) {
	code, out, _ := runWith(t, []string{"--list-engines", "-f", "jsonl"}, listReg(), "")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("jsonl: %d lines, want 3 (one engine per line)", len(lines))
	}
	for i, l := range lines {
		var e format.ListEngine
		if err := json.Unmarshal([]byte(l), &e); err != nil {
			t.Errorf("jsonl line %d not an engine object: %v: %s", i, err, l)
		}
	}
}

func TestRun_ListEngines_Text(t *testing.T) {
	code, out, _ := runWith(t, []string{"--list-engines", "-f", "text"}, listReg(), "")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	for _, want := range []string{"NAME", "brave", "missing-key", "mojeek", "ready", "ddg", "disabled", "keyless"} {
		if !strings.Contains(out, want) {
			t.Errorf("text list missing %q in:\n%s", want, out)
		}
	}
}

func TestRun_ListEngines_TableDeferred(t *testing.T) {
	code, out, errs := runWith(t, []string{"--list-engines", "-f", "table"}, listReg(), "")
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (table is v0.2)", code)
	}
	if out != "" {
		t.Errorf("stdout must be empty on exit 2, got %q", out)
	}
	if !strings.Contains(errs, "table") || !strings.Contains(errs, "v0.2") {
		t.Errorf("expected table/v0.2 message, got %s", errs)
	}
}

// TestRun_ListEngines_WeightFlagReflected: --weight overrides the displayed
// effective weight (§4: "weight (effective, after config + flag merge)").
func TestRun_ListEngines_WeightFlagReflected(t *testing.T) {
	code, out, _ := runWith(t, []string{"--list-engines", "--weight", "brave=5.5"}, listReg(), "")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	var entries []format.ListEngine
	json.Unmarshal([]byte(out), &entries)
	if brave := findEntry(t, entries, "brave"); brave.Weight != 5.5 {
		t.Errorf("--weight brave=5.5 not reflected: brave.Weight = %v", brave.Weight)
	}
	// Other engines keep their default weight (not a full reset — §4).
	if m := findEntry(t, entries, "mojeek"); m.Weight != 1.3 {
		t.Errorf("mojeek weight changed to %v, want 1.3 (no reset)", m.Weight)
	}
}

// TestRun_ListEngines_ExitsWithoutQuery: --list-engines short-circuits before
// query resolution, so it works with no positional query at all.
func TestRun_ListEngines_ExitsWithoutQuery(t *testing.T) {
	code, out, errs := runWith(t, []string{"--list-engines"}, listReg(), "")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (no query needed); stderr=%s", code, errs)
	}
	if !strings.Contains(out, "brave") {
		t.Errorf("expected listing output, got %s", out)
	}
}

// === Brave engine integration (httptest) ====================================

// TestRun_Brave_E2E exercises the full pipeline with a real brave engine
// backed by an httptest server: dispatch → rank → format → exit code.
func TestRun_Brave_E2E(t *testing.T) {
	fixture := map[string]any{
		"web": map[string]any{
			"results": []map[string]any{
				{"title": "Go Concurrency", "url": "https://go.dev/doc/concurrency", "snippet": "Go concurrency model."},
				{"title": "Goroutines", "url": "https://go.dev/blog/concurrency", "snippet": "Introduction to goroutines."},
			},
		},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(fixture)
	}))
	defer ts.Close()

	// Point brave at the test server.
	origBase := brave.APIBase()
	brave.SetAPIBase(ts.URL + "/")
	defer brave.SetAPIBase(origBase)

	t.Setenv("BRAVE_API_KEY", "test-key")

	reg := engine.NewRegistry()
	if err := reg.Register(brave.New()); err != nil {
		t.Fatal(err)
	}

	code, out, errs := runWith(t, []string{"-n", "5", "go concurrency"}, reg, "")
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, errs)
	}

	var env format.Envelope
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("stdout not valid JSON: %v\n%s", err, out)
	}
	if env.Schema != 1 {
		t.Errorf("schema = %d, want 1", env.Schema)
	}
	if len(env.Results) != 2 {
		t.Fatalf("results = %d, want 2", len(env.Results))
	}
	// Brave results should carry through the ranker.
	if env.Results[0].Title != "Go Concurrency" {
		t.Errorf("top result title = %q, want Go Concurrency", env.Results[0].Title)
	}
	if env.Results[0].URL != "https://go.dev/doc/concurrency" {
		t.Errorf("top result url = %q, want https://go.dev/doc/concurrency", env.Results[0].URL)
	}
	// The engines block should show brave as ok.
	if bs, ok := env.Engines["brave"]; !ok || !bs.OK {
		t.Errorf("engines block: brave = %+v, want ok=true", env.Engines["brave"])
	}
}

// TestRun_Wikipedia_E2E exercises the full pipeline with a real wikipedia engine
// backed by an httptest server: dispatch → rank → format → exit code.
func TestRun_Wikipedia_E2E(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Wikipedia OpenSearch API shape: [query, [titles], [descriptions], [urls]]
		w.Write([]byte(`["test query",["Wikipedia Article"],["A description."],["https://en.wikipedia.org/wiki/Article"]]`))
	}))
	defer ts.Close()

	origBase := wikipedia.APIBase()
	wikipedia.SetAPIBase(ts.URL)
	defer wikipedia.SetAPIBase(origBase)

	reg := engine.NewRegistry()
	if err := reg.Register(wikipedia.New()); err != nil {
		t.Fatal(err)
	}

	code, out, errs := runWith(t, []string{"-n", "5", "test query"}, reg, "")
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, errs)
	}

	var env format.Envelope
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("stdout not valid JSON: %v\n%s", err, out)
	}
	if env.Schema != 1 {
		t.Errorf("schema = %d, want 1", env.Schema)
	}
	if len(env.Results) != 1 {
		t.Fatalf("results = %d, want 1", len(env.Results))
	}
	if env.Results[0].Title != "Wikipedia Article" {
		t.Errorf("top result title = %q, want Wikipedia Article", env.Results[0].Title)
	}
	if env.Results[0].URL != "https://en.wikipedia.org/wiki/Article" {
		t.Errorf("top result url = %q, want https://en.wikipedia.org/wiki/Article", env.Results[0].URL)
	}
	if bs, ok := env.Engines["wikipedia"]; !ok || !bs.OK {
		t.Errorf("engines block: wikipedia = %+v, want ok=true", env.Engines["wikipedia"])
	}
}

// TestRun_DDG_E2E exercises the full pipeline with a real ddg engine
// backed by an httptest server: dispatch → rank → format → exit code.
func TestRun_DDG_E2E(t *testing.T) {
	fixture := `<html><body>` +
		`<div class="result">` +
		`  <a class="result__a" href="https://example.com/ddg-result">` +
		`    <h2 class="result__title">DDG Result</h2>` +
		`    <p class="result__snippet">DuckDuckGo snippet.</p>` +
		`  </a>` +
		`</div>` +
		`</body></html>`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(fixture))
	}))
	defer ts.Close()

	origBase := ddg.ScrapeBase()
	ddg.SetScrapeBase(ts.URL)
	defer ddg.SetScrapeBase(origBase)

	reg := engine.NewRegistry()
	if err := reg.Register(ddg.New()); err != nil {
		t.Fatal(err)
	}

	// ddg is off by default — must explicitly enable via --engine.
	code, out, errs := runWith(t, []string{"-e", "ddg", "-n", "5", "test query"}, reg, "")
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, errs)
	}

	var env format.Envelope
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("stdout not valid JSON: %v\n%s", err, out)
	}
	if len(env.Results) != 1 {
		t.Fatalf("results = %d, want 1", len(env.Results))
	}
	if env.Results[0].Title != "DDG Result" {
		t.Errorf("top result title = %q, want DDG Result", env.Results[0].Title)
	}
	if bs, ok := env.Engines["ddg"]; !ok || !bs.OK {
		t.Errorf("engines block: ddg = %+v, want ok=true", env.Engines["ddg"])
	}
}

// TestRun_DDG_OffByDefault verifies ddg is disabled by default.
func TestRun_DDG_OffByDefault(t *testing.T) {
	// Use a fake engine that reports enabled=false (mimicking ddg's descriptor).
	ddgFake := &fakeEngine{
		name:    "ddg",
		weight:  0.5,
		enabled: false, // off by default
		results: []engine.Result{r("D", "https://x/ddg")},
	}
	reg := newReg(ddgFake)
	// Without --engine ddg, ddg should be auto-skipped (enabled=false).
	code, out, _ := runWith(t, []string{"q"}, reg, "")
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (ddg off by default, no results)", code)
	}
	if strings.Contains(out, "ddg") {
		t.Errorf("ddg should not appear in output when off by default: %s", out)
	}
	// With --engine ddg, it should run.
	code2, out2, _ := runWith(t, []string{"-e", "ddg", "q"}, reg, "")
	if code2 != 0 {
		t.Fatalf("exit = %d, want 0 (--engine ddg enables it)", code2)
	}
	if !strings.Contains(out2, "https://x/ddg") {
		t.Errorf("ddg result should appear with --engine: %s", out2)
	}
}

// === helpers ================================================================

func assertStructuredError(t *testing.T, stderr string, wantCode string, wantExit int) {
	t.Helper()
	var se format.StructuredError
	if err := json.Unmarshal([]byte(strings.TrimSpace(stderr)), &se); err != nil {
		t.Fatalf("stderr is not a structured error object: %v\n%s", err, stderr)
	}
	if se.Error.Code != wantCode || se.Error.Exit != wantExit {
		t.Errorf("structured error = %+v, want code=%q exit=%d", se, wantCode, wantExit)
	}
}

func sliceEq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func floatEq(a, b float64) bool {
	const tol = 1e-12
	return a-b < tol && b-a < tol
}
