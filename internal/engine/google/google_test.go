package google

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/openmtx/meta-search-tool/internal/engine"
	"github.com/openmtx/meta-search-tool/internal/engine/retry"
)

// fastRetry makes the §11 backoff instant + deterministic for retry tests.
func fastRetry(t *testing.T) {
	t.Helper()
	retry.SetBackoffOverride(func(int) time.Duration { return 0 })
	retry.SetJitterSource(func(time.Duration) time.Duration { return 0 })
}

// fixtureJSON returns a valid Google Custom Search API JSON response.
func fixtureJSON(items ...string) string {
	if len(items) == 0 {
		return `{"items":[]}`
	}
	itemsJSON := `[` + strings.Join(items, ",") + `]`
	return `{"items":` + itemsJSON + `}`
}

// itemJSON returns a single item JSON fragment.
func itemJSON(title, link, snippet string) string {
	b, _ := json.Marshal(map[string]string{
		"title":   title,
		"link":    link,
		"snippet": snippet,
	})
	return string(b)
}

// === Descriptor =============================================================

func TestDescriptor(t *testing.T) {
	g := New()
	info := g.Descriptor()
	if info.Name != "google" {
		t.Errorf("Name = %q, want google", info.Name)
	}
	if !info.Keyed {
		t.Error("Keyed should be true")
	}
	if info.KeyEnv != "GOOGLE_API_KEY" {
		t.Errorf("KeyEnv = %q, want GOOGLE_API_KEY", info.KeyEnv)
	}
	if info.DefaultWeight != 1.0 {
		t.Errorf("DefaultWeight = %v, want 1.0", info.DefaultWeight)
	}
	if info.Enabled {
		t.Error("Enabled should be false (opt-in)")
	}
}

// === Golden-table parse (pure function) =====================================

func TestParseResults(t *testing.T) {
	resp := apiResponse{
		Items: []apiItem{
			{Title: "Result One", Link: "https://example.com/1", Snippet: "Snippet one."},
			{Title: "Result Two", Link: "https://example.com/2", Snippet: "Snippet two."},
		},
	}
	results := parseResults(resp)
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}
	if results[0].Title != "Result One" || results[0].URL != "https://example.com/1" {
		t.Errorf("result[0] = %+v, want title=Result One url=https://example.com/1", results[0])
	}
	if results[0].Snippet != "Snippet one." {
		t.Errorf("snippet[0] = %q, want Snippet one.", results[0].Snippet)
	}
}

func TestParseResults_Empty(t *testing.T) {
	resp := apiResponse{Items: []apiItem{}}
	results := parseResults(resp)
	if len(results) != 0 {
		t.Errorf("results = %d, want 0", len(results))
	}
}

func TestParseResults_SkipEmptyURL(t *testing.T) {
	resp := apiResponse{
		Items: []apiItem{
			{Title: "No Link", Link: "", Snippet: "skip"},
			{Title: "Has Link", Link: "https://example.com/y", Snippet: "keep"},
		},
	}
	results := parseResults(resp)
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	if results[0].Title != "Has Link" {
		t.Errorf("title = %q, want Has Link", results[0].Title)
	}
}

// === httptest round-trip ====================================================

// TestSearch_Httptest verifies GET request mechanics, auth params, and full
// pipeline (fetch → parse → Result slice).
func TestSearch_Httptest(t *testing.T) {
	t.Setenv("GOOGLE_API_KEY", "test-api-key")
	t.Setenv("GOOGLE_CX", "test-cx")

	fixture := fixtureJSON(
		itemJSON("Go Programming", "https://go.dev/", "The Go programming language."),
		itemJSON("Go Wiki", "https://github.com/golang/go/wiki", "Unofficial Go wiki."),
	)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.Header.Get("Accept") != "application/json" {
			t.Errorf("Accept = %q, want application/json", r.Header.Get("Accept"))
		}
		if r.Header.Get("User-Agent") != "mst/dev" {
			t.Errorf("User-Agent = %q, want mst/dev", r.Header.Get("User-Agent"))
		}
		// Verify query params.
		q := r.URL.Query()
		if q.Get("key") != "test-api-key" {
			t.Errorf("key = %q, want test-api-key", q.Get("key"))
		}
		if q.Get("cx") != "test-cx" {
			t.Errorf("cx = %q, want test-cx", q.Get("cx"))
		}
		if q.Get("q") != "go language" {
			t.Errorf("q = %q, want go language", q.Get("q"))
		}
		if q.Get("num") != "10" {
			t.Errorf("num = %q, want 10", q.Get("num"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(fixture))
	}))
	defer ts.Close()

	origBase := APIBase()
	SetAPIBase(ts.URL)
	defer SetAPIBase(origBase)

	g := New()
	results, err := g.Search(context.Background(), "go language", engine.Options{Limit: 10})
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}
	if results[0].Title != "Go Programming" {
		t.Errorf("result[0].Title = %q, want Go Programming", results[0].Title)
	}
	if results[0].URL != "https://go.dev/" {
		t.Errorf("result[0].URL = %q, want https://go.dev/", results[0].URL)
	}
}

// === buildRequestURL ========================================================

func TestBuildRequestURL(t *testing.T) {
	tests := []struct {
		name    string
		q       string
		key     string
		cx      string
		limit   int
		wantNum string
	}{
		{"normal", "go", "k1", "cx1", 10, "10"},
		{"cap at 10", "test", "k2", "cx2", 20, "10"},
		{"floor at 1", "test", "k3", "cx3", 0, "1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, err := buildRequestURL(tt.q, tt.key, tt.cx, tt.limit)
			if err != nil {
				t.Fatalf("buildRequestURL error: %v", err)
			}
			parsed, err := url.Parse(u)
			if err != nil {
				t.Fatalf("failed to parse result URL: %v", err)
			}
			q := parsed.Query()
			if q.Get("key") != tt.key {
				t.Errorf("key = %q, want %q", q.Get("key"), tt.key)
			}
			if q.Get("cx") != tt.cx {
				t.Errorf("cx = %q, want %q", q.Get("cx"), tt.cx)
			}
			if q.Get("q") != tt.q {
				t.Errorf("q = %q, want %q", q.Get("q"), tt.q)
			}
			if q.Get("num") != tt.wantNum {
				t.Errorf("num = %q, want %q", q.Get("num"), tt.wantNum)
			}
		})
	}
}

// === Error paths ============================================================

func TestSearch_NoKey(t *testing.T) {
	os.Unsetenv("GOOGLE_API_KEY")
	os.Unsetenv("GOOGLE_CX")
	g := New()
	results, err := g.Search(context.Background(), "test", engine.Options{Limit: 10})
	if err == nil {
		t.Fatal("expected error when no key/CX set")
	}
	if !strings.Contains(err.Error(), "GOOGLE_API_KEY") {
		t.Errorf("error should mention GOOGLE_API_KEY: %v", err)
	}
	if results != nil {
		t.Error("expected nil results")
	}
}

func TestSearch_ContextCancelled(t *testing.T) {
	t.Setenv("GOOGLE_API_KEY", "k")
	t.Setenv("GOOGLE_CX", "cx")
	g := New()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	results, err := g.Search(ctx, "test", engine.Options{Limit: 10})
	if err == nil {
		t.Fatal("expected error on cancelled context")
	}
	if results != nil {
		t.Error("expected nil results")
	}
}

func TestSearch_HTTPError(t *testing.T) {
	t.Setenv("GOOGLE_API_KEY", "k")
	t.Setenv("GOOGLE_CX", "cx")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	origBase := APIBase()
	SetAPIBase(ts.URL)
	defer SetAPIBase(origBase)

	g := New()
	results, err := g.Search(context.Background(), "test", engine.Options{Limit: 10})
	if err == nil {
		t.Fatal("expected error on HTTP 404")
	}
	if results != nil {
		t.Error("expected nil results on HTTP 404")
	}
	if !strings.Contains(err.Error(), "google") {
		t.Errorf("error should mention google: %v", err)
	}
}

func TestSearch_APIAuthError(t *testing.T) {
	t.Setenv("GOOGLE_API_KEY", "bad-key")
	t.Setenv("GOOGLE_CX", "bad-cx")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"error":{"message":"API key not valid.","code":403}}`))
	}))
	defer ts.Close()

	origBase := APIBase()
	SetAPIBase(ts.URL)
	defer SetAPIBase(origBase)

	g := New()
	results, err := g.Search(context.Background(), "test", engine.Options{Limit: 10})
	if err == nil {
		t.Fatal("expected API auth error")
	}
	if !strings.Contains(err.Error(), "API key not valid") {
		t.Errorf("error should mention API key invalid: %v", err)
	}
	if results != nil {
		t.Error("expected nil results on API error")
	}
}

func TestSearch_MalformedResponse(t *testing.T) {
	t.Setenv("GOOGLE_API_KEY", "k")
	t.Setenv("GOOGLE_CX", "cx")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{invalid json`))
	}))
	defer ts.Close()

	origBase := APIBase()
	SetAPIBase(ts.URL)
	defer SetAPIBase(origBase)

	g := New()
	results, err := g.Search(context.Background(), "test", engine.Options{Limit: 10})
	if err == nil {
		t.Fatal("expected parse error on malformed response")
	}
	if results != nil {
		t.Error("expected nil results")
	}
}

// === Retry tests ============================================================

func TestSearch_Retry_429SucceedsOnRetry(t *testing.T) {
	t.Setenv("GOOGLE_API_KEY", "k")
	t.Setenv("GOOGLE_CX", "cx")
	attempts := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Write([]byte(fixtureJSON(itemJSON("R1", "https://e.com/1", "S1."))))
	}))
	defer ts.Close()

	origBase := APIBase()
	SetAPIBase(ts.URL)
	defer SetAPIBase(origBase)
	fastRetry(t)

	g := New()
	results, err := g.Search(context.Background(), "test", engine.Options{Limit: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if attempts != 2 {
		t.Errorf("attempts = %d, want 2 (initial + retry)", attempts)
	}
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
}

func TestSearch_Retry_5xxSucceedsOnRetry(t *testing.T) {
	t.Setenv("GOOGLE_API_KEY", "k")
	t.Setenv("GOOGLE_CX", "cx")
	attempts := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Write([]byte(fixtureJSON(itemJSON("R1", "https://e.com/1", "S1."))))
	}))
	defer ts.Close()

	origBase := APIBase()
	SetAPIBase(ts.URL)
	defer SetAPIBase(origBase)
	fastRetry(t)

	g := New()
	results, err := g.Search(context.Background(), "test", engine.Options{Limit: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if attempts != 2 {
		t.Errorf("attempts = %d, want 2 (initial + retry)", attempts)
	}
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
}

func TestSearch_Retry_Exhausted(t *testing.T) {
	t.Setenv("GOOGLE_API_KEY", "k")
	t.Setenv("GOOGLE_CX", "cx")
	attempts := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer ts.Close()

	origBase := APIBase()
	SetAPIBase(ts.URL)
	defer SetAPIBase(origBase)
	fastRetry(t)

	g := New()
	results, err := g.Search(context.Background(), "test", engine.Options{Limit: 10})
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	if results != nil {
		t.Error("expected nil results after exhausted retries")
	}
	if attempts != 3 {
		t.Errorf("attempts = %d, want 3 (initial + 2 retries)", attempts)
	}
}

func TestSearch_Retry_ContextCancelledDuringBackoff(t *testing.T) {
	t.Setenv("GOOGLE_API_KEY", "k")
	t.Setenv("GOOGLE_CX", "cx")
	var attempts int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer ts.Close()

	origBase := APIBase()
	SetAPIBase(ts.URL)
	defer SetAPIBase(origBase)
	fastRetry(t)

	ctx, cancel := context.WithCancel(context.Background())
	g := New()
	done := make(chan struct{})
	go func() {
		g.Search(ctx, "q", engine.Options{Limit: 10})
		close(done)
	}()

	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// OK — Search returned promptly after cancel.
	case <-time.After(2 * time.Second):
		t.Fatal("Search did not return after context cancel during backoff")
	}
	if attempts < 2 {
		t.Errorf("HTTP request count = %d, want >= 2", attempts)
	}
}

func TestSearch_Retry_NonRetryableStatus(t *testing.T) {
	t.Setenv("GOOGLE_API_KEY", "k")
	t.Setenv("GOOGLE_CX", "cx")
	attempts := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer ts.Close()

	origBase := APIBase()
	SetAPIBase(ts.URL)
	defer SetAPIBase(origBase)
	fastRetry(t)

	g := New()
	results, err := g.Search(context.Background(), "test", engine.Options{Limit: 10})
	if err == nil {
		t.Fatal("expected error on HTTP 401")
	}
	if results != nil {
		t.Error("expected nil results on HTTP 401")
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1 (no retry for 401)", attempts)
	}
}

// === SearchWithRaw ==========================================================

func TestSearchWithRaw(t *testing.T) {
	t.Setenv("GOOGLE_API_KEY", "k")
	t.Setenv("GOOGLE_CX", "cx")
	fixture := `{"items":[{"title":"T","link":"https://e.com/x","snippet":"S"}],"extraField":true}`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(fixture))
	}))
	defer ts.Close()

	origBase := APIBase()
	SetAPIBase(ts.URL)
	defer SetAPIBase(origBase)

	re, ok := New().(engine.RawEngine)
	if !ok {
		t.Fatal("google must implement engine.RawEngine")
	}
	results, raw, err := re.SearchWithRaw(context.Background(), "q", engine.Options{Limit: 10})
	if err != nil {
		t.Fatalf("SearchWithRaw: %v", err)
	}
	if len(results) != 1 || results[0].Title != "T" {
		t.Errorf("results = %+v, want 1 result Title=T", results)
	}
	rawMap, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("raw payload = %T, want map[string]any", raw)
	}
	if rawMap["extraField"] != true {
		t.Errorf("raw missing extraField: %v", rawMap)
	}
}
