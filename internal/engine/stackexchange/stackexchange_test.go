package stackexchange

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

// minimalAPIResponse is a valid Stack Exchange API response with 2 items.
const minimalAPIResponse = `{
  "items": [
    {
      "question_id": 12345,
      "title": "How do I use goroutines?",
      "link": "https://stackoverflow.com/questions/12345",
      "body": "<p>Goroutines are lightweight threads managed by the Go runtime.</p>",
      "score": 42,
      "is_accepted": true,
      "creation_date": 1609459200
    },
    {
      "question_id": 67890,
      "title": "Understanding Go channels",
      "link": "https://stackoverflow.com/questions/67890",
      "body": "<p>Channels are the pipes that connect concurrent goroutines.</p>",
      "score": 35,
      "is_accepted": false,
      "creation_date": 1612137600
    }
  ],
  "quota_remaining": 299,
  "has_more": false,
  "rate_limit": 300
}`

// emptyAPIResponse has no items.
const emptyAPIResponse = `{
  "items": [],
  "quota_remaining": 299,
  "has_more": false,
  "rate_limit": 300
}`

// errorAPIResponse has an error message.
const errorAPIResponse = `{
  "items": [],
  "error_message": "bad request",
  "quota_remaining": 298,
  "has_more": false,
  "rate_limit": 300
}`

// malformedJSON is not valid JSON.
const malformedJSON = `not json at all <><`

func TestDescriptor(t *testing.T) {
	e := New()
	d := e.Descriptor()
	if d.Name != "stackexchange" {
		t.Errorf("Name = %q, want %q", d.Name, "stackexchange")
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
	if !d.Enabled {
		t.Error("Enabled should be true")
	}
}

func TestParseResults(t *testing.T) {
	var resp apiResponse
	if err := json.Unmarshal([]byte(minimalAPIResponse), &resp); err != nil {
		t.Fatalf("failed to parse fixture: %v", err)
	}

	results, err := parseResults(resp.Items)
	if err != nil {
		t.Fatalf("parseResults error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}

	want := []engine.Result{
		{
			Title:   "How do I use goroutines?",
			URL:     "https://stackoverflow.com/questions/12345",
			Snippet: "Goroutines are lightweight threads managed by the Go runtime.",
		},
		{
			Title:   "Understanding Go channels",
			URL:     "https://stackoverflow.com/questions/67890",
			Snippet: "Channels are the pipes that connect concurrent goroutines.",
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
	results, err := parseResults(resp.Items)
	if err != nil {
		t.Fatalf("parseResults error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("got %d results, want 0", len(results))
	}
}

func TestParseResults_MissingLink(t *testing.T) {
	items := []apiItem{
		{QuestionID: 1, Title: "No Link", Body: "body"},
	}
	results, err := parseResults(items)
	if err != nil {
		t.Fatalf("parseResults error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("got %d results, want 0 (missing link)", len(results))
	}
}

func TestParseResults_MissingTitle(t *testing.T) {
	items := []apiItem{
		{QuestionID: 1, Link: "https://stackoverflow.com/questions/1"},
	}
	results, err := parseResults(items)
	if err != nil {
		t.Fatalf("parseResults error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("got %d results, want 0 (missing title)", len(results))
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
		if parsed.Path != "/search/advanced" {
			t.Errorf("path = %s, want /search/advanced", parsed.Path)
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

	apiBase = ts.URL + "/2"

	e := New()
	results, err := e.Search(context.Background(), "go goroutines", engine.Options{Limit: 10})
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
	_, err := e.Search(ctx, "goroutines", engine.Options{Limit: 10})
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
	results, err := e.Search(context.Background(), "goroutines", engine.Options{Limit: 10})
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
	results, err := e.Search(context.Background(), "goroutines", engine.Options{Limit: 10})
	if err == nil {
		t.Fatal("expected error for malformed response")
	}
	if results != nil {
		t.Error("expected nil results on parse failure")
	}
}

func TestSearch_APIError(t *testing.T) {
	orig := apiBase
	defer func() { apiBase = orig }()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(errorAPIResponse))
	}))
	defer ts.Close()

	apiBase = ts.URL

	e := New()
	results, err := e.Search(context.Background(), "goroutines", engine.Options{Limit: 10})
	if err == nil {
		t.Fatal("expected error for API error response")
	}
	if results != nil {
		t.Error("expected nil results on API error")
	}
}

// --- buildRequestURL --------------------------------------------------------

func TestBuildRequestURL(t *testing.T) {
	tests := []struct {
		name    string
		q       string
		limit   int
		wantQ   string
		wantMax int
	}{
		{
			name:    "normal query",
			q:       "go goroutines",
			limit:   10,
			wantQ:   "go goroutines",
			wantMax: 10,
		},
		{
			name:    "limit capped at 100",
			q:       "kubernetes deployment",
			limit:   200,
			wantQ:   "kubernetes deployment",
			wantMax: 100,
		},
		{
			name:    "limit floored at 1",
			q:       "rust lifetimes",
			limit:   0,
			wantQ:   "rust lifetimes",
			wantMax: 1,
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
			// The path is relative to apiBase; default base includes "/2".
			wantPath := "/search/advanced"
			if parsed.Path != wantPath {
				t.Errorf("path = %s, want %s", parsed.Path, wantPath)
			}
			q := parsed.Query()
			if q.Get("q") != tt.wantQ {
				t.Errorf("q = %q, want %q", q.Get("q"), tt.wantQ)
			}
			if q.Get("site") != "stackoverflow" {
				t.Errorf("site = %q, want stackoverflow", q.Get("site"))
			}
			if q.Get("order") != "desc" {
				t.Errorf("order = %q, want desc", q.Get("order"))
			}
			if q.Get("sort") != "relevance" {
				t.Errorf("sort = %q, want relevance", q.Get("sort"))
			}
			if q.Get("maxcount") != "" {
				var max int
				_, err := fmt.Sscanf(q.Get("maxcount"), "%d", &max)
				if err != nil {
					t.Fatalf("invalid maxcount: %v", err)
				}
				if max != tt.wantMax {
					t.Errorf("maxcount = %d, want %d", max, tt.wantMax)
				}
			} else {
				t.Error("maxcount is empty")
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

	apiBase = ts.URL + "/2"

	e := New()
	results, err := e.Search(context.Background(), "goroutines", engine.Options{Limit: 10})
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
	results, err := e.Search(context.Background(), "goroutines", engine.Options{Limit: 10})
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
