package grokipedia

import (
	"context"
	"encoding/json"
	"fmt"
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

// minimalAPIResponse is a valid Grokipedia API response with 2 results.
const minimalAPIResponse = `{
  "results": [
    {
      "slug": "go-programming-language",
      "title": "Go Programming Language",
      "snippet": "Go is an open source programming language created at Google."
    },
    {
      "slug": "concurrency-in-go",
      "title": "Concurrency in Go",
      "snippet": "Go provides goroutines and channels for concurrent programming."
    }
  ]
}`

// emptyAPIResponse has no results.
const emptyAPIResponse = `{
  "results": []
}`

// malformedJSON is not valid JSON.
const malformedJSON = `not json at all <><`

func TestDescriptor(t *testing.T) {
	e := New()
	d := e.Descriptor()
	if d.Name != "grokipedia" {
		t.Errorf("Name = %q, want %q", d.Name, "grokipedia")
	}
	if d.Keyed {
		t.Error("Keyed should be false")
	}
	if d.KeyEnv != "" {
		t.Errorf("KeyEnv = %q, want empty", d.KeyEnv)
	}
	if d.DefaultWeight != 1.0 {
		t.Errorf("DefaultWeight = %v, want 1.0", d.DefaultWeight)
	}
	if d.Enabled {
		t.Error("Enabled should be false (experimental)")
	}
}

func TestParseResults(t *testing.T) {
	var resp apiResponse
	if err := json.Unmarshal([]byte(minimalAPIResponse), &resp); err != nil {
		t.Fatalf("failed to parse fixture: %v", err)
	}

	results, err := parseResults(resp.Results)
	if err != nil {
		t.Fatalf("parseResults error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}

	want := []engine.Result{
		{
			Title:   "Go Programming Language",
			URL:     "https://grokipedia.com/page/go-programming-language",
			Snippet: "Go is an open source programming language created at Google.",
		},
		{
			Title:   "Concurrency in Go",
			URL:     "https://grokipedia.com/page/concurrency-in-go",
			Snippet: "Go provides goroutines and channels for concurrent programming.",
		},
	}

	for i, w := range want {
		if results[i].Title != w.Title {
			t.Errorf("[%d] Title = %q, want %q", i, results[i].Title, w.Title)
		}
		if results[i].URL != w.URL {
			t.Errorf("[%d] URL = %q, want %q", i, results[i].URL, w.URL)
		}
		if results[i].Snippet != w.Snippet {
			t.Errorf("[%d] Snippet = %q, want %q", i, results[i].Snippet, w.Snippet)
		}
	}
}

func TestParseResults_Empty(t *testing.T) {
	var resp apiResponse
	if err := json.Unmarshal([]byte(emptyAPIResponse), &resp); err != nil {
		t.Fatalf("failed to parse fixture: %v", err)
	}
	results, err := parseResults(resp.Results)
	if err != nil {
		t.Fatalf("parseResults error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("got %d results, want 0", len(results))
	}
}

func TestParseResults_MissingSlug(t *testing.T) {
	// Result with no slug — URL cannot be constructed.
	results, err := parseResults([]apiResult{
		{Title: "No Slug Title", Snippet: "Some snippet"},
	})
	if err != nil {
		t.Fatalf("parseResults error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("got %d results, want 0 (missing slug)", len(results))
	}
}

func TestParseResults_MissingTitle(t *testing.T) {
	results, err := parseResults([]apiResult{
		{Slug: "some-page", Snippet: "Some snippet"},
	})
	if err != nil {
		t.Fatalf("parseResults error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("got %d results, want 0 (missing title)", len(results))
	}
}

func TestParseResults_HTMLSnippet(t *testing.T) {
	// Snippets arrive with HTML markup (SearXNG applies html_to_text); tags must
	// be stripped to plain text (spec §5).
	results, err := parseResults([]apiResult{
		{Slug: "go", Title: "Go", Snippet: "<p>Go is <b>open source</b>.</p>"},
	})
	if err != nil {
		t.Fatalf("parseResults error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if results[0].Snippet != "Go is open source." {
		t.Errorf("snippet = %q, want %q (HTML stripped)", results[0].Snippet, "Go is open source.")
	}
}

func TestParseResults_MalformedJSON(t *testing.T) {
	var resp apiResponse
	err := json.Unmarshal([]byte(malformedJSON), &resp)
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

// --- httptest round-trip ----------------------------------------------------

func TestSearch_Httptest(t *testing.T) {
	orig := apiBase
	defer func() { apiBase = orig }()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		parsed, err := url.Parse(r.URL.String())
		if err != nil {
			t.Fatalf("failed to parse request URL: %v", err)
		}
		if parsed.Path != "/api/full-text-search" {
			t.Errorf("path = %s, want /api/full-text-search", parsed.Path)
		}
		ua := r.Header.Get("User-Agent")
		if ua == "" {
			t.Error("missing User-Agent header")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(minimalAPIResponse))
	}))
	defer ts.Close()

	apiBase = ts.URL

	e := New()
	results, err := e.Search(context.Background(), "go concurrency", engine.Options{Limit: 10})
	if err != nil {
		t.Fatalf("Search error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
}

func TestSearch_ContextCancelled(t *testing.T) {
	e := New()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately
	_, err := e.Search(ctx, "go", engine.Options{Limit: 10})
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestSearch_HTTPError(t *testing.T) {
	orig := apiBase
	defer func() { apiBase = orig }()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte("502 Bad Gateway"))
	}))
	defer ts.Close()

	apiBase = ts.URL

	e := New()
	results, err := e.Search(context.Background(), "go", engine.Options{Limit: 10})
	if err == nil {
		t.Fatal("expected error for HTTP 502")
	}
	if results != nil {
		t.Error("expected nil results on HTTP error")
	}
}

func TestSearch_MalformedResponse(t *testing.T) {
	orig := apiBase
	defer func() { apiBase = orig }()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(malformedJSON))
	}))
	defer ts.Close()

	apiBase = ts.URL

	e := New()
	results, err := e.Search(context.Background(), "go", engine.Options{Limit: 10})
	if err == nil {
		t.Fatal("expected error for malformed response")
	}
	if results != nil {
		t.Error("expected nil results on parse failure")
	}
}

// --- SearchWithRaw round-trip ------------------------------------------------

func TestSearchWithRaw_Httptest(t *testing.T) {
	orig := apiBase
	defer func() { apiBase = orig }()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(minimalAPIResponse))
	}))
	defer ts.Close()

	apiBase = ts.URL

	e := New()
	rawEngine, ok := e.(engine.RawEngine)
	if !ok {
		t.Fatal("expected engine to implement RawEngine")
	}
	results, raw, err := rawEngine.SearchWithRaw(context.Background(), "go", engine.Options{Limit: 10})
	if err != nil {
		t.Fatalf("SearchWithRaw error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	if raw == nil {
		t.Fatal("expected raw response, got nil")
	}
	// Verify raw is a decodable map.
	rawMap, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("raw type = %T, want map[string]any", raw)
	}
	if _, hasResults := rawMap["results"]; !hasResults {
		t.Error("raw response missing 'results' key")
	}
}

// --- buildRequestURL --------------------------------------------------------

func TestBuildRequestURL(t *testing.T) {
	tests := []struct {
		name  string
		q     string
		limit int
		wantQ string
		wantL int
	}{
		{
			name:  "normal query",
			q:     "go concurrency",
			limit: 10,
			wantQ: "go concurrency",
			wantL: 10,
		},
		{
			name:  "limit capped at 100",
			q:     "kubernetes operators",
			limit: 500,
			wantQ: "kubernetes operators",
			wantL: 100,
		},
		{
			name:  "limit floored at 1",
			q:     "rust ownership",
			limit: 0,
			wantQ: "rust ownership",
			wantL: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, err := buildRequestURL(tt.q, tt.limit)
			if err != nil {
				t.Fatalf("buildRequestURL error: %v", err)
			}
			parsed, err := url.Parse(u)
			if err != nil {
				t.Fatalf("failed to parse result URL: %v", err)
			}
			if parsed.Path != "/api/full-text-search" {
				t.Errorf("path = %s, want /api/full-text-search", parsed.Path)
			}
			q := parsed.Query()
			if q.Get("query") != tt.wantQ {
				t.Errorf("query = %q, want %q", q.Get("query"), tt.wantQ)
			}
			limitStr := q.Get("limit")
			if limitStr == "" {
				t.Fatal("limit is empty")
			}
			var l int
			_, err = fmt.Sscanf(limitStr, "%d", &l)
			if err != nil {
				t.Fatalf("invalid limit: %v", err)
			}
			if l != tt.wantL {
				t.Errorf("limit = %d, want %d", l, tt.wantL)
			}
		})
	}
}

// --- Retry tests (shared policy via internal/engine/retry) -------------------

func fastRetry(t *testing.T) func() {
	t.Helper()
	retry.SetBackoffOverride(func(attempt int) time.Duration {
		return 10 * time.Millisecond // fast for tests
	})
	retry.SetJitterSource(nil) // deterministic
	return func() { retry.SetBackoffOverride(nil) }
}

func TestSearch_Retry_429SucceedsOnRetry(t *testing.T) {
	defer fastRetry(t)()

	orig := apiBase
	defer func() { apiBase = orig }()

	count := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		if count == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(minimalAPIResponse))
	}))
	defer ts.Close()

	apiBase = ts.URL

	e := New()
	results, err := e.Search(context.Background(), "go", engine.Options{Limit: 10})
	if err != nil {
		t.Fatalf("Search error after retry: %v", err)
	}
	if count != 2 {
		t.Errorf("got %d requests, want 2 (initial + 1 retry)", count)
	}
	if len(results) != 2 {
		t.Errorf("got %d results, want 2", len(results))
	}
}

func TestSearch_Retry_Exhausted(t *testing.T) {
	defer fastRetry(t)()

	orig := apiBase
	defer func() { apiBase = orig }()

	count := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer ts.Close()

	apiBase = ts.URL

	e := New()
	results, err := e.Search(context.Background(), "go", engine.Options{Limit: 10})
	if err == nil {
		t.Fatal("expected error after retry exhaustion")
	}
	if results != nil {
		t.Error("expected nil results on retry exhaustion")
	}
	if count != 3 {
		t.Errorf("got %d requests, want 3 (initial + 2 retries)", count)
	}
}

// --- Live integration test (§14, MST_ITEST=1) -------------------------------

// TestSearch_Live verifies the engine against the real grokipedia.com endpoint —
// confirming the request URL (path + params), response schema, and HTML-snippet
// stripping. Skipped unless MST_ITEST=1. This is the de-risker that would have
// caught the /api path bug found in review.
func TestSearch_Live(t *testing.T) {
	if os.Getenv("MST_ITEST") != "1" {
		t.Skip("skipping live test; set MST_ITEST=1 to hit grokipedia.com")
	}
	e := New()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	results, err := e.Search(ctx, "go programming language", engine.Options{Limit: 10})
	if err != nil {
		t.Fatalf("live grokipedia search failed (endpoint/schema may have drifted): %v", err)
	}
	if len(results) == 0 {
		t.Fatal("live grokipedia search returned 0 results")
	}
	t.Logf("grokipedia live returned %d results; first: %q <%s> — %s",
		len(results), results[0].Title, results[0].URL, results[0].Snippet)

	for _, r := range results {
		if !strings.HasPrefix(r.URL, "https://grokipedia.com/page/") {
			t.Errorf("unexpected result URL (want /page/<slug>): %s", r.URL)
		}
		if strings.Contains(r.Snippet, "<") {
			t.Errorf("snippet still contains HTML markup after stripping: %q", r.Snippet)
		}
	}
}
