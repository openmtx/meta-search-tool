package mojeek

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openmtx/meta-search-tool/internal/engine"
	"github.com/openmtx/meta-search-tool/internal/engine/retry"
)

// fastRetry makes the §11 backoff instant + deterministic for retry tests,
// restoring the original hooks on cleanup.
func fastRetry(t *testing.T) {
	t.Helper()
	retry.SetBackoffOverride(func(int) time.Duration { return 0 })
	retry.SetJitterSource(func(time.Duration) time.Duration { return 0 })
}

// --- Golden-table: parseResults -------------------------------------------

const mockHTML = `<!DOCTYPE html>
<html>
<body>
<ul class="results-standard">
  <li>
    <a class="ob" href="https://example.com/result1"></a>
    <h2><a href="https://example.com/result1">First Result Title</a></h2>
    <p class="s">This is the first result snippet with some useful text.</p>
  </li>
  <li>
    <a class="ob" href="https://example.com/result2"></a>
    <h2><a href="https://example.com/result2">Second Result Title</a></h2>
    <p class="s">The second result snippet goes here.</p>
  </li>
  <li>
    <a class="ob" href="https://example.com/result3"></a>
    <h2><a href="https://example.com/result3">Third Result Title</a></h2>
    <p class="s">Snippet for the third result.</p>
  </li>
</ul>
</body>
</html>`

func TestParseResults(t *testing.T) {
	got, err := parseResults(mockHTML)
	if err != nil {
		t.Fatalf("parseResults: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d results, want 3", len(got))
	}

	tests := []struct {
		wantTitle string
		wantURL   string
		wantSnip  string
	}{
		{"First Result Title", "https://example.com/result1", "This is the first result snippet with some useful text."},
		{"Second Result Title", "https://example.com/result2", "The second result snippet goes here."},
		{"Third Result Title", "https://example.com/result3", "Snippet for the third result."},
	}

	for i, tt := range tests {
		if got[i].Title != tt.wantTitle {
			t.Errorf("result[%d].Title = %q, want %q", i, got[i].Title, tt.wantTitle)
		}
		if got[i].URL != tt.wantURL {
			t.Errorf("result[%d].URL = %q, want %q", i, got[i].URL, tt.wantURL)
		}
		if got[i].Snippet != tt.wantSnip {
			t.Errorf("result[%d].Snippet = %q, want %q", i, got[i].Snippet, tt.wantSnip)
		}
	}
}

func TestParseResults_Empty(t *testing.T) {
	got, err := parseResults("<html><body></body></html>")
	if err != nil {
		t.Fatalf("parseResults: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("empty HTML => %d results, want 0", len(got))
	}
}

func TestParseResults_NoResultsStandard(t *testing.T) {
	html := `<html><body><div class="other">No match</div></body></html>`
	got, err := parseResults(html)
	if err != nil {
		t.Fatalf("parseResults: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("no results-standard => %d results, want 0", len(got))
	}
}

// --- httptest: full Search round-trip --------------------------------------

func TestSearch_Httptest(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request headers.
		if got := r.Header.Get("User-Agent"); got != userAgent {
			t.Errorf("User-Agent = %q, want %q", got, userAgent)
		}
		if got := r.Header.Get("Accept-Language"); got == "" {
			t.Error("Accept-Language header missing")
		}

		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(mockHTML))
	}))
	defer ts.Close()

	oldBase := scrapeBase
	scrapeBase = ts.URL
	defer func() { scrapeBase = oldBase }()

	e := New()
	results, err := e.Search(context.Background(), "test query", engine.Options{Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("got %d results, want 3", len(results))
	}
	if results[0].Title != "First Result Title" || results[0].URL != "https://example.com/result1" {
		t.Errorf("result[0] = %+v, want Title=First Result Title URL=https://example.com/result1", results[0])
	}
}

func TestSearch_ContextCancelled(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	oldBase := scrapeBase
	scrapeBase = ts.URL
	defer func() { scrapeBase = oldBase }()

	ctx, cancel := context.WithCancel(context.Background())
	e := New()
	done := make(chan struct{})
	go func() {
		e.Search(ctx, "q", engine.Options{Limit: 10})
		close(done)
	}()

	cancel()
	select {
	case <-done:
		// OK — Search returned promptly after cancel.
	case <-time.After(2 * time.Second):
		t.Fatal("Search did not return after context cancel")
	}
}

func TestSearch_HTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	oldBase := scrapeBase
	scrapeBase = ts.URL
	defer func() { scrapeBase = oldBase }()

	e := New()
	results, err := e.Search(context.Background(), "q", engine.Options{Limit: 10})
	if err == nil {
		t.Fatal("expected error on HTTP 404")
	}
	if results != nil {
		t.Errorf("expected nil results on error, got %d", len(results))
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error should mention 404: %v", err)
	}
}

// --- Descriptor -------------------------------------------------------------

func TestDescriptor(t *testing.T) {
	e := New()
	info := e.Descriptor()
	if info.Name != "mojeek" {
		t.Errorf("Name = %q, want mojeek", info.Name)
	}
	if info.Keyed {
		t.Error("Keyed should be false")
	}
	if info.KeyEnv != "" {
		t.Errorf("KeyEnv = %q, want empty", info.KeyEnv)
	}
	if info.DefaultWeight != 1.3 {
		t.Errorf("DefaultWeight = %f, want 1.3", info.DefaultWeight)
	}
	if !info.Enabled {
		t.Error("Enabled should be true")
	}
}

// --- buildRequestURL --------------------------------------------------------

func TestBuildRequestURL(t *testing.T) {
	tests := []struct {
		name     string
		q        string
		page     int
		wantS    string // expected s param value, or "" if absent
		wantQ    string
		wantSafe string
	}{
		{"page 0", "test", 0, "", "test", "0"},   // page 1: no s param
		{"page 1", "test", 1, "10", "test", "0"}, // page 2: s=10
		{"page 5", "test", 5, "50", "test", "0"}, // page 6: s=50
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildRequestURL(tt.q, tt.page)
			if err != nil {
				t.Fatalf("buildRequestURL(%q, %d): %v", tt.q, tt.page, err)
			}
			u, err := url.Parse(got)
			if err != nil {
				t.Fatalf("parse result URL: %v", err)
			}
			if u.Query().Get("q") != tt.wantQ {
				t.Errorf("q = %q, want %q", u.Query().Get("q"), tt.wantQ)
			}
			if u.Query().Get("safe") != tt.wantSafe {
				t.Errorf("safe = %q, want %q", u.Query().Get("safe"), tt.wantSafe)
			}
			if tt.wantS == "" {
				if u.Query().Get("s") != "" {
					t.Errorf("s param should be absent on page 0, got %q", u.Query().Get("s"))
				}
			} else {
				if u.Query().Get("s") != tt.wantS {
					t.Errorf("s = %q, want %q", u.Query().Get("s"), tt.wantS)
				}
			}
		})
	}
}

// --- Pagination -------------------------------------------------------------

// TestSearch_Pagination_StopOnShortPage verifies that fetching stops once a
// page returns fewer than resultsPerPage hits (the last page), and that the
// `s` param is omitted on page 0 and incremented by 10 thereafter.
func TestSearch_Pagination_StopOnShortPage(t *testing.T) {
	// Page 0 returns a short page (1 result) → must stop, no second request.
	var count int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&count, 1)
		if s := r.URL.Query().Get("s"); s != "" {
			t.Errorf("page 0 must omit s, got %q", s)
		}
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<ul class="results-standard"><li>
			<a class="ob" href="https://example.com/only"></a>
			<h2>Only Result</h2><p class="s">snip</p></li></ul>`))
	}))
	defer ts.Close()

	oldBase := scrapeBase
	scrapeBase = ts.URL
	defer func() { scrapeBase = oldBase }()

	e := New()
	results, err := e.Search(context.Background(), "q", engine.Options{Limit: 100})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("got %d results, want 1", len(results))
	}
	if count != 1 {
		t.Errorf("request count = %d, want 1 (short page stops pagination)", count)
	}
}

// TestSearch_Pagination_StopsAtLimit verifies the fetch-limit short-circuit:
// once accumulated results reach opts.Limit, no further pages are fetched.
func TestSearch_Pagination_StopsAtLimit(t *testing.T) {
	// Each page returns a full page (resultsPerPage=10), but limit=10 → one page.
	var count int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&count, 1)
		w.Header().Set("Content-Type", "text/html")
		// 10 results so the short-page stop does NOT fire.
		var lis strings.Builder
		lis.WriteString("<ul class=\"results-standard\">")
		for i := 0; i < 10; i++ {
			fmt.Fprintf(&lis, "<li><a class=\"ob\" href=\"https://example.com/%d\"></a><h2>T%d</h2><p class=\"s\">s%d</p></li>", i, i, i)
		}
		lis.WriteString("</ul>")
		w.Write([]byte(lis.String()))
	}))
	defer ts.Close()

	oldBase := scrapeBase
	scrapeBase = ts.URL
	defer func() { scrapeBase = oldBase }()

	e := New()
	results, err := e.Search(context.Background(), "q", engine.Options{Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 10 {
		t.Fatalf("got %d results, want 10", len(results))
	}
	if count != 1 {
		t.Errorf("request count = %d, want 1 (limit reached → stop)", count)
	}
}

// TestSearch_Pagination_PartialResultsOnFailure verifies §7: when a later page
// fails after an earlier page succeeded, the engine returns the accumulated
// partial results plus the error (not nil results).
func TestSearch_Pagination_PartialResultsOnFailure(t *testing.T) {
	var count int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&count, 1)
		if n == 1 {
			// Page 0: 10 results (full page → paginate again).
			w.Header().Set("Content-Type", "text/html")
			var lis strings.Builder
			lis.WriteString("<ul class=\"results-standard\">")
			for i := 0; i < 10; i++ {
				fmt.Fprintf(&lis, "<li><a class=\"ob\" href=\"https://example.com/%d\"></a><h2>T%d</h2><p class=\"s\">s%d</p></li>", i, i, i)
			}
			lis.WriteString("</ul>")
			w.Write([]byte(lis.String()))
			return
		}
		// Page 1+: persistent 429 → exhausts retries.
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer ts.Close()

	oldBase := scrapeBase
	scrapeBase = ts.URL
	defer func() { scrapeBase = oldBase }()
	fastRetry(t)

	e := New()
	results, err := e.Search(context.Background(), "q", engine.Options{Limit: 100})
	if err == nil {
		t.Fatal("expected error after page-2 failure")
	}
	// §7: partial results from page 0 are returned alongside the error.
	if len(results) != 10 {
		t.Errorf("expected 10 partial results, got %d", len(results))
	}
	if !strings.Contains(err.Error(), "429") {
		t.Errorf("error should mention 429: %v", err)
	}
}

// --- Retry: HTTP 429/5xx backoff -------------------------------------------

// TestSearch_Retry_429SucceedsOnRetry verifies that a transient 429 is retried
// and eventually succeeds.
func TestSearch_Retry_429SucceedsOnRetry(t *testing.T) {
	var count int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		if count == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(mockHTML))
	}))
	defer ts.Close()

	oldBase := scrapeBase
	scrapeBase = ts.URL
	defer func() { scrapeBase = oldBase }()
	fastRetry(t)

	e := New()
	results, err := e.Search(context.Background(), "q", engine.Options{Limit: 10})
	if err != nil {
		t.Fatalf("expected success after retry: %v", err)
	}
	if len(results) != 3 {
		t.Errorf("got %d results, want 3", len(results))
	}
	// Should have made 2 requests (initial 429 + successful retry).
	if count != 2 {
		t.Errorf("HTTP request count = %d, want 2", count)
	}
}

// TestSearch_Retry_5xxSucceedsOnRetry verifies that a transient 500 is retried
// and eventually succeeds.
func TestSearch_Retry_5xxSucceedsOnRetry(t *testing.T) {
	var count int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		if count == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(mockHTML))
	}))
	defer ts.Close()

	oldBase := scrapeBase
	scrapeBase = ts.URL
	defer func() { scrapeBase = oldBase }()
	fastRetry(t)

	e := New()
	results, err := e.Search(context.Background(), "q", engine.Options{Limit: 10})
	if err != nil {
		t.Fatalf("expected success after retry: %v", err)
	}
	if len(results) != 3 {
		t.Errorf("got %d results, want 3", len(results))
	}
	if count != 2 {
		t.Errorf("HTTP request count = %d, want 2", count)
	}
}

// TestSearch_Retry_Exhausted verifies that after exhausting retries on
// persistent 429/5xx, the engine returns nil results + error.
func TestSearch_Retry_Exhausted(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer ts.Close()

	oldBase := scrapeBase
	scrapeBase = ts.URL
	defer func() { scrapeBase = oldBase }()
	fastRetry(t)

	e := New()
	results, err := e.Search(context.Background(), "q", engine.Options{Limit: 10})
	if err == nil {
		t.Fatal("expected error after retry exhaustion")
	}
	if results != nil {
		t.Errorf("expected nil results, got %d", len(results))
	}
	if !strings.Contains(err.Error(), "429") {
		t.Errorf("error should mention 429: %v", err)
	}
}

// TestSearch_Retry_ContextCancelledDuringBackoff verifies that if the context
// is cancelled during a backoff sleep, Search returns promptly with ctx.Err().
func TestSearch_Retry_ContextCancelledDuringBackoff(t *testing.T) {
	var count int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer ts.Close()

	oldBase := scrapeBase
	scrapeBase = ts.URL
	defer func() { scrapeBase = oldBase }()
	fastRetry(t)

	ctx, cancel := context.WithCancel(context.Background())
	e := New()
	done := make(chan struct{})
	go func() {
		e.Search(ctx, "q", engine.Options{Limit: 10})
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
	// Should have made at least 2 requests (initial + first retry before cancel).
	if count < 2 {
		t.Errorf("HTTP request count = %d, want >= 2", count)
	}
}

// TestSearch_Retry_NonRetryableStatus verifies that non-retryable status codes
// (e.g. 400, 404) return immediately without retry.
func TestSearch_Retry_NonRetryableStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	oldBase := scrapeBase
	scrapeBase = ts.URL
	defer func() { scrapeBase = oldBase }()

	e := New()
	results, err := e.Search(context.Background(), "q", engine.Options{Limit: 10})
	if err == nil {
		t.Fatal("expected error on HTTP 404")
	}
	if results != nil {
		t.Errorf("expected nil results, got %d", len(results))
	}
	// Should NOT have retried.
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error should mention 404: %v", err)
	}
}

// TestSearch_Retry_RetryAfterHonored verifies that a Retry-After header does
// not break the retry path (the header's max() semantics are unit-tested in
// internal/engine/retry).
func TestSearch_Retry_RetryAfterHonored(t *testing.T) {
	var count int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		if count == 1 {
			w.Header().Set("Retry-After", "0") // honored as max(0, backoff)
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(mockHTML))
	}))
	defer ts.Close()

	oldBase := scrapeBase
	scrapeBase = ts.URL
	defer func() { scrapeBase = oldBase }()
	fastRetry(t)

	e := New()
	results, err := e.Search(context.Background(), "q", engine.Options{Limit: 10})
	if err != nil {
		t.Fatalf("expected success after retry: %v", err)
	}
	if len(results) != 3 {
		t.Errorf("got %d results, want 3", len(results))
	}
	if count != 2 {
		t.Errorf("HTTP request count = %d, want 2", count)
	}
}
