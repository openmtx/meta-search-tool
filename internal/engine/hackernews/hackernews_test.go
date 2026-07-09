package hackernews

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/openmtx/meta-search-tool/internal/engine"
	"github.com/openmtx/meta-search-tool/internal/engine/retry"
)

// minimalAPIResponse is a valid HN Algolia API response with 2 hits.
const minimalAPIResponse = `{
  "hits": [
    {
      "title": "Show HN: My new Go project",
      "url": "https://example.com",
      "points": 42,
      "created_at": "2024-01-15T10:30:00Z",
      "objectID": "39001001"
    },
    {
      "title": "How to build a search engine in Go",
      "url": "https://blog.example.com/search-engine-go",
      "points": 128,
      "created_at": "2024-02-20T14:00:00Z",
      "objectID": "39001002"
    }
  ],
  "nbHits": 2,
  "page": 0,
  "nbPages": 1,
  "hitsPerPage": 10
}`

// emptyAPIResponse has no hits.
const emptyAPIResponse = `{
  "hits": [],
  "nbHits": 0,
  "page": 0,
  "nbPages": 0,
  "hitsPerPage": 10
}`

// malformedJSON is not valid JSON.
const malformedJSON = `not json at all <><`

func TestDescriptor(t *testing.T) {
	e := New()
	d := e.Descriptor()
	if d.Name != "hackernews" {
		t.Errorf("Name = %q, want %q", d.Name, "hackernews")
	}
	if d.Keyed {
		t.Error("Keyed should be false")
	}
	if d.KeyEnv != "" {
		t.Errorf("KeyEnv = %q, want empty", d.KeyEnv)
	}
	if d.DefaultWeight != 0.8 {
		t.Errorf("DefaultWeight = %v, want 0.8", d.DefaultWeight)
	}
	if !d.Enabled {
		t.Error("Enabled should be true")
	}
}

func TestParseResults(t *testing.T) {
	var resp apiResponse
	if err := json.Unmarshal([]byte(minimalAPIResponse), &resp); err != nil {
		t.Fatalf("failed to parse fixture: %v", err)
	}

	results, err := parseResults(resp.Hits)
	if err != nil {
		t.Fatalf("parseResults error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}

	want := []engine.Result{
		{
			Title:   "Show HN: My new Go project",
			URL:     "https://example.com",
			Snippet: "2024-01-15 · 42 points",
		},
		{
			Title:   "How to build a search engine in Go",
			URL:     "https://blog.example.com/search-engine-go",
			Snippet: "2024-02-20 · 128 points",
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
	results, err := parseResults(resp.Hits)
	if err != nil {
		t.Fatalf("parseResults error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("got %d results, want 0", len(results))
	}
}

func TestParseResults_MissingURL(t *testing.T) {
	// Hit with no URL and no objectID — should be skipped.
	hits := []apiHit{
		{Title: "No URL Hit"},
	}
	results, err := parseResults(hits)
	if err != nil {
		t.Fatalf("parseResults error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("got %d results, want 0 (missing URL and objectID)", len(results))
	}
}

func TestParseResults_MissingTitle(t *testing.T) {
	hits := []apiHit{
		{URL: "https://example.com"},
	}
	results, err := parseResults(hits)
	if err != nil {
		t.Fatalf("parseResults error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("got %d results, want 0 (missing title)", len(results))
	}
}

func TestParseResults_ConstructURLFromObjectID(t *testing.T) {
	hits := []apiHit{
		{
			Title:     "HN Item via objectID",
			Points:    15,
			CreatedAt: "2024-03-01T08:00:00Z",
			ObjectID:  "12345",
		},
	}
	results, err := parseResults(hits)
	if err != nil {
		t.Fatalf("parseResults error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	wantURL := "https://news.ycombinator.com/item?id=12345"
	if results[0].URL != wantURL {
		t.Errorf("URL = %q, want %q", results[0].URL, wantURL)
	}
	wantSnippet := "2024-03-01 · 15 points"
	if results[0].Snippet != wantSnippet {
		t.Errorf("Snippet = %q, want %q", results[0].Snippet, wantSnippet)
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
		// buildRequestURL sets base.Path = "/search"; the apiBase already includes
		// "/api/v1", so the final path is just "/search".
		if parsed.Path != "/search" {
			t.Errorf("path = %s, want /search", parsed.Path)
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

	apiBase = ts.URL + "/api/v1"

	e := New()
	results, err := e.Search(context.Background(), "go search engine", engine.Options{Limit: 10})
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

// --- buildRequestURL --------------------------------------------------------

func TestBuildRequestURL(t *testing.T) {
	tests := []struct {
		name   string
		q      string
		limit  int
		wantQ  string
		wantHP int
	}{
		{
			name:   "normal query",
			q:      "go concurrency",
			limit:  10,
			wantQ:  "go concurrency",
			wantHP: 10,
		},
		{
			name:   "limit capped at 1000",
			q:      "kubernetes operators",
			limit:  5000,
			wantQ:  "kubernetes operators",
			wantHP: 1000,
		},
		{
			name:   "limit floored at 1",
			q:      "rust ownership",
			limit:  0,
			wantQ:  "rust ownership",
			wantHP: 1,
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
			if parsed.Path != "/search" {
				t.Errorf("path = %s, want /search", parsed.Path)
			}
			q := parsed.Query()
			if q.Get("query") != tt.wantQ {
				t.Errorf("query = %q, want %q", q.Get("query"), tt.wantQ)
			}
			if q.Get("tags") != "story" {
				t.Errorf("tags = %q, want story", q.Get("tags"))
			}
			if q.Get("hitsPerPage") != "" {
				var hp int
				_, err := fmt.Sscanf(q.Get("hitsPerPage"), "%d", &hp)
				if err != nil {
					t.Fatalf("invalid hitsPerPage: %v", err)
				}
				if hp != tt.wantHP {
					t.Errorf("hitsPerPage = %d, want %d", hp, tt.wantHP)
				}
			} else {
				t.Error("hitsPerPage is empty")
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

	apiBase = ts.URL + "/api/v1"

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
