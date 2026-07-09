package arxiv

import (
	"context"
	"encoding/xml"
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

// minimalAtomFeed is a minimal arXiv Atom feed fixture with 2 entries.
const minimalAtomFeed = `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <entry>
    <id>http://arxiv.org/abs/1706.03762</id>
    <title>Attention Is All You Need</title>
    <summary>We introduce a new simple network architecture, the Transformer, based solely on attention mechanisms.</summary>
    <link rel="alternate" href="https://arxiv.org/abs/1706.03762"/>
  </entry>
  <entry>
    <id>http://arxiv.org/abs/1512.03385</id>
    <title>Deep Residual Learning for Image Recognition</title>
    <summary>Deeper neural networks are difficult to train. We present a residual learning framework.</summary>
    <link rel="alternate" href="https://arxiv.org/abs/1512.03385"/>
  </entry>
</feed>`

// emptyAtomFeed is an arXiv Atom feed with no entries.
const emptyAtomFeed = `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
</feed>`

// malformedXML is not valid XML.
const malformedXML = `not xml at all <><`

func TestDescriptor(t *testing.T) {
	e := New()
	d := e.Descriptor()
	if d.Name != "arxiv" {
		t.Errorf("Name = %q, want %q", d.Name, "arxiv")
	}
	if d.Keyed {
		t.Error("Keyed should be false")
	}
	if d.KeyEnv != "" {
		t.Errorf("KeyEnv = %q, want empty", d.KeyEnv)
	}
	if d.DefaultWeight != 1.2 {
		t.Errorf("DefaultWeight = %v, want 1.2", d.DefaultWeight)
	}
	if !d.Enabled {
		t.Error("Enabled should be true")
	}
}

func TestParseResults(t *testing.T) {
	var feed atomFeed
	if err := xml.Unmarshal([]byte(minimalAtomFeed), &feed); err != nil {
		t.Fatalf("failed to parse fixture: %v", err)
	}

	results, err := parseResults(feed)
	if err != nil {
		t.Fatalf("parseResults error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}

	want := []engine.Result{
		{Title: "Attention Is All You Need", URL: "https://arxiv.org/abs/1706.03762", Snippet: "We introduce a new simple network architecture, the Transformer, based solely on attention mechanisms."},
		{Title: "Deep Residual Learning for Image Recognition", URL: "https://arxiv.org/abs/1512.03385", Snippet: "Deeper neural networks are difficult to train. We present a residual learning framework."},
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
	var feed atomFeed
	if err := xml.Unmarshal([]byte(emptyAtomFeed), &feed); err != nil {
		t.Fatalf("failed to parse fixture: %v", err)
	}
	results, err := parseResults(feed)
	if err != nil {
		t.Fatalf("parseResults error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("got %d results, want 0", len(results))
	}
}

func TestParseResults_MissingAlternateLink(t *testing.T) {
	// Entry with no rel="alternate" link.
	feed := atomFeed{
		Entries: []atomEntry{
			{ID: "http://arxiv.org/abs/1234.5678", Title: "No Link Entry", Summary: "summary"},
		},
	}
	results, err := parseResults(feed)
	if err != nil {
		t.Fatalf("parseResults error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("got %d results, want 0 (no alternate link)", len(results))
	}
}

func TestParseResults_MalformedXML(t *testing.T) {
	var feed atomFeed
	err := xml.Unmarshal([]byte(malformedXML), &feed)
	if err == nil {
		t.Fatal("expected error for malformed XML")
	}
}

// --- httptest round-trip ----------------------------------------------------

func TestSearch_Httptest(t *testing.T) {
	orig := apiBase
	defer func() { apiBase = orig }()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request.
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		ua := r.Header.Get("User-Agent")
		if ua == "" {
			t.Error("missing User-Agent header")
		}
		w.Header().Set("Content-Type", "application/atom+xml")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(minimalAtomFeed))
	}))
	defer ts.Close()

	apiBase = ts.URL

	e := New()
	results, err := e.Search(context.Background(), "transformer attention", engine.Options{Limit: 10})
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
	_, err := e.Search(ctx, "transformer", engine.Options{Limit: 10})
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
	results, err := e.Search(context.Background(), "transformer", engine.Options{Limit: 10})
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
		w.Header().Set("Content-Type", "application/atom+xml")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(malformedXML))
	}))
	defer ts.Close()

	apiBase = ts.URL

	e := New()
	results, err := e.Search(context.Background(), "transformer", engine.Options{Limit: 10})
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
		name    string
		q       string
		limit   int
		wantQ   string
		wantMax int
	}{
		{
			name:    "normal query (terms AND'd)",
			q:       "transformer attention",
			limit:   10,
			wantQ:   "all:transformer AND all:attention",
			wantMax: 10,
		},
		{
			name:    "limit capped at 3000",
			q:       "quantum computing",
			limit:   5000,
			wantQ:   "all:quantum AND all:computing",
			wantMax: 3000,
		},
		{
			name:    "limit floored at 1",
			q:       "deep learning",
			limit:   0,
			wantQ:   "all:deep AND all:learning",
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
			q := parsed.Query()
			if q.Get("search_query") != tt.wantQ {
				t.Errorf("search_query = %q, want %q", q.Get("search_query"), tt.wantQ)
			}
			if q.Get("max_results") != "" {
				var max int
				_, err := fmt.Sscanf(q.Get("max_results"), "%d", &max)
				if err != nil {
					t.Fatalf("invalid max_results: %v", err)
				}
				if max != tt.wantMax {
					t.Errorf("max_results = %d, want %d", max, tt.wantMax)
				}
			} else {
				t.Error("max_results is empty")
			}
		})
	}
}

// --- Retry tests (shared policy via internal/engine/retry) -------------------

func fastRetry(t *testing.T) func() {
	t.Helper()
	var orig func(int) time.Duration
	orig = retry.CalcBackoff // capture via a trick: we need to restore the override
	_ = orig
	retry.SetBackoffOverride(func(attempt int) time.Duration {
		return 10 * time.Millisecond // fast for tests
	})
	retry.SetJitterSource(nil) // deterministic
	return func() { retry.SetBackoffOverride(nil) }
}

// Note: the retry package is shared, so we test via httptest that retries work.
// The retry.Do function in internal/engine/retry handles the policy; we verify
// it by checking that a 429→200 sequence succeeds.

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
		w.Header().Set("Content-Type", "application/atom+xml")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(minimalAtomFeed))
	}))
	defer ts.Close()

	apiBase = ts.URL

	e := New()
	results, err := e.Search(context.Background(), "transformer", engine.Options{Limit: 10})
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
	results, err := e.Search(context.Background(), "transformer", engine.Options{Limit: 10})
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

// TestSearch_Live verifies the engine against the real arXiv endpoint,
// including the AND-term query construction (an out-of-domain query should
// yield ~0 hits, not a noise flood). Skipped unless MST_ITEST=1.
func TestSearch_Live(t *testing.T) {
	if os.Getenv("MST_ITEST") != "1" {
		t.Skip("skipping live test; set MST_ITEST=1 to hit export.arxiv.org")
	}
	e := New()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// In-domain academic query → relevant results.
	results, err := e.Search(ctx, "transformer attention", engine.Options{Limit: 5})
	if err != nil {
		t.Fatalf("live arxiv search failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("live arxiv search returned 0 results for an in-domain query")
	}
	t.Logf("arxiv live (in-domain): %d results; first: %q", len(results), results[0].Title)
	for _, r := range results {
		if !strings.HasPrefix(r.URL, "http://arxiv.org/abs/") && !strings.HasPrefix(r.URL, "https://arxiv.org/abs/") {
			t.Errorf("unexpected result URL: %s", r.URL)
		}
	}

	// Out-of-domain natural-language query → should yield few/zero, NOT a flood
	// of loosely-matched papers (the bug this AND construction fixes).
	noise, err := e.Search(ctx, "Erling Haaland diet", engine.Options{Limit: 20})
	if err != nil {
		t.Fatalf("live arxiv out-of-domain search failed: %v", err)
	}
	if len(noise) > 5 {
		t.Errorf("out-of-domain query returned %d results (want ~0; AND-term construction should suppress noise): first=%q",
			len(noise), func() string {
				if len(noise) > 0 {
					return noise[0].Title
				}
				return ""
			}())
	}
}
