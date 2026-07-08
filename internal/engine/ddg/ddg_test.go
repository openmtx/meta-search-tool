package ddg

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/openmtx/meta-search-tool/internal/engine"
	"github.com/openmtx/meta-search-tool/internal/engine/retry"
)

// fastRetry makes the §11 backoff instant + deterministic for retry tests,
// restoring the original hooks on cleanup. Without this the retry tests would
// sleep the real exponential backoff (~1.5–3s each).
func fastRetry(t *testing.T) {
	t.Helper()
	retry.SetBackoffOverride(func(int) time.Duration { return 0 })
	retry.SetJitterSource(func(time.Duration) time.Duration { return 0 })
}

// === Descriptor =============================================================

func TestDescriptor(t *testing.T) {
	e := New()
	info := e.Descriptor()
	if info.Name != "ddg" {
		t.Errorf("Name = %q, want ddg", info.Name)
	}
	if info.Keyed {
		t.Error("Keyed should be false")
	}
	if info.KeyEnv != "" {
		t.Errorf("KeyEnv = %q, want empty", info.KeyEnv)
	}
	if info.DefaultWeight != 0.5 {
		t.Errorf("DefaultWeight = %v, want 0.5", info.DefaultWeight)
	}
	if info.Enabled {
		t.Error("Enabled should be false (off by default)")
	}
}

// === Golden-table parse (pure function) =====================================

func TestParseResults(t *testing.T) {
	html := `<html><body>` +
		`<div class="result">` +
		`  <a class="result__a" href="https://example.com/article1">` +
		`    <h2 class="result__title">Article One</h2>` +
		`    <p class="result__snippet">This is the first article snippet.</p>` +
		`  </a>` +
		`</div>` +
		`<div class="result">` +
		`  <a class="result__a" href="https://example.com/article2">` +
		`    <h2 class="result__title">Article Two</h2>` +
		`    <p class="result__snippet">This is the second article snippet.</p>` +
		`  </a>` +
		`</div>` +
		`</body></html>`

	results, err := parseResults(html)
	if err != nil {
		t.Fatalf("parseResults error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}
	if results[0].Title != "Article One" || results[0].URL != "https://example.com/article1" {
		t.Errorf("result[0] = %+v, want title=Article One url=https://example.com/article1", results[0])
	}
	if results[0].Snippet != "This is the first article snippet." {
		t.Errorf("snippet[0] = %q, want This is the first article snippet.", results[0].Snippet)
	}
	if results[1].Title != "Article Two" {
		t.Errorf("result[1] title = %q, want Article Two", results[1].Title)
	}
}

func TestParseResults_Empty(t *testing.T) {
	// Empty div.result with empty href — goquery finds the div but the href check skips it.
	html := `<html><body><div class="result"><a class="result__a" href=""></a></div></body></html>`
	results, err := parseResults(html)
	if err != nil {
		t.Fatalf("parseResults error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("results = %d, want 0 (empty href)", len(results))
	}
}

func TestParseResults_NoResultsStandard(t *testing.T) {
	html := `<html><body><div class="no-results">No results found</div></body></html>`
	results, err := parseResults(html)
	if err != nil {
		t.Fatalf("parseResults error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("results = %d, want 0 (no result divs)", len(results))
	}
}

// === httptest round-trip ====================================================

func TestSearch_Httptest(t *testing.T) {
	fixture := `<html><body>` +
		`<div class="result">` +
		`  <a class="result__a" href="https://en.wikipedia.org/wiki/Golang">` +
		`    <h2 class="result__title">Golang</h2>` +
		`    <p class="result__snippet">Go programming language.</p>` +
		`  </a>` +
		`</div>` +
		`<div class="result">` +
		`  <a class="result__a" href="https://go.dev/doc/">` +
		`    <h2 class="result__title">Go Documentation</h2>` +
		`    <p class="result__snippet">Official Go documentation.</p>` +
		`  </a>` +
		`</div>` +
		`</body></html>`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request.
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.Header.Get("User-Agent") != userAgent {
			t.Errorf("User-Agent = %q, want %q", r.Header.Get("User-Agent"), userAgent)
		}
		// r.URL.Query().Get() returns the decoded value.
		q := r.URL.Query().Get("q")
		if q != "test query" {
			t.Errorf("q param = %q, want test query (decoded)", q)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(fixture))
	}))
	defer ts.Close()

	origBase := ScrapeBase()
	SetScrapeBase(ts.URL)
	defer SetScrapeBase(origBase)

	e := New()
	results, err := e.Search(context.Background(), "test query", engine.Options{Limit: 10})
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}
	if results[0].Title != "Golang" {
		t.Errorf("result[0] title = %q, want Golang", results[0].Title)
	}
	if results[0].URL != "https://en.wikipedia.org/wiki/Golang" {
		t.Errorf("result[0] url = %q, want https://en.wikipedia.org/wiki/Golang", results[0].URL)
	}
	if results[0].Snippet != "Go programming language." {
		t.Errorf("snippet[0] = %q, want Go programming language.", results[0].Snippet)
	}
}

// === buildRequestURL ========================================================

func TestBuildRequestURL(t *testing.T) {
	tests := []struct {
		name  string
		q     string
		wantQ string // decoded value (r.URL.Query().Get() returns decoded)
	}{
		{"normal", "go programming", "go programming"},
		{"spaces", "hello world", "hello world"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			origBase := ScrapeBase()
			SetScrapeBase("https://example.com/search")
			defer SetScrapeBase(origBase)

			s, err := buildRequestURL(tt.q)
			if err != nil {
				t.Fatalf("buildRequestURL error: %v", err)
			}
			u, err := url.Parse(s)
			if err != nil {
				t.Fatalf("parse URL: %v", err)
			}
			if u.Query().Get("q") != tt.wantQ {
				t.Errorf("q = %q, want %q", u.Query().Get("q"), tt.wantQ)
			}
		})
	}
}

// === Error paths ============================================================

func TestSearch_ContextCancelled(t *testing.T) {
	e := New()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately
	results, err := e.Search(ctx, "test", engine.Options{Limit: 10})
	if err == nil {
		t.Fatal("expected error on cancelled context")
	}
	if results != nil {
		t.Error("expected nil results on cancelled context")
	}
}

func TestSearch_HTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	origBase := ScrapeBase()
	SetScrapeBase(ts.URL)
	defer SetScrapeBase(origBase)

	e := New()
	results, err := e.Search(context.Background(), "test", engine.Options{Limit: 10})
	if err == nil {
		t.Fatal("expected error on HTTP 404")
	}
	if results != nil {
		t.Error("expected nil results on HTTP 404")
	}
	if !strings.Contains(err.Error(), "ddg") {
		t.Errorf("error should mention ddg: %v", err)
	}
}

// === Retry tests ============================================================

func TestSearch_Retry_429SucceedsOnRetry(t *testing.T) {
	attempts := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Write([]byte(`<html><body><div class="result"><a class="result__a" href="https://example.com/1"><h2 class="result__title">Result 1</h2><p class="result__snippet">Snippet.</p></a></div></body></html>`))
	}))
	defer ts.Close()

	origBase := ScrapeBase()
	SetScrapeBase(ts.URL)
	defer SetScrapeBase(origBase)
	fastRetry(t)

	e := New()
	results, err := e.Search(context.Background(), "test", engine.Options{Limit: 10})
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
	attempts := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Write([]byte(`<html><body><div class="result"><a class="result__a" href="https://example.com/1"><h2 class="result__title">Result 1</h2><p class="result__snippet">Snippet.</p></a></div></body></html>`))
	}))
	defer ts.Close()

	origBase := ScrapeBase()
	SetScrapeBase(ts.URL)
	defer SetScrapeBase(origBase)
	fastRetry(t)

	e := New()
	results, err := e.Search(context.Background(), "test", engine.Options{Limit: 10})
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
	attempts := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer ts.Close()

	origBase := ScrapeBase()
	SetScrapeBase(ts.URL)
	defer SetScrapeBase(origBase)
	fastRetry(t)

	e := New()
	results, err := e.Search(context.Background(), "test", engine.Options{Limit: 10})
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
	var attempts int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer ts.Close()

	origBase := ScrapeBase()
	SetScrapeBase(ts.URL)
	defer SetScrapeBase(origBase)
	fastRetry(t)

	ctx, cancel := context.WithCancel(context.Background())
	e := New()
	done := make(chan struct{})
	go func() {
		e.Search(ctx, "q", engine.Options{Limit: 10})
		close(done)
	}()

	// Let the first retry's backoff start, then cancel.
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// OK — Search returned promptly after cancel.
	case <-time.After(2 * time.Second):
		t.Fatal("Search did not return after context cancel during backoff")
	}
	// Should have made at least 2 requests (initial + first retry before cancel).
	if attempts < 2 {
		t.Errorf("HTTP request count = %d, want >= 2", attempts)
	}
}

func TestSearch_Retry_NonRetryableStatus(t *testing.T) {
	attempts := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer ts.Close()

	origBase := ScrapeBase()
	SetScrapeBase(ts.URL)
	defer SetScrapeBase(origBase)
	fastRetry(t)

	e := New()
	results, err := e.Search(context.Background(), "test", engine.Options{Limit: 10})
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

// TestSearchWithRaw verifies ddg implements engine.RawEngine and that the raw
// payload is the response HTML body (the native payload for a scrape engine).
func TestSearchWithRaw(t *testing.T) {
	htmlBody := `<html><body>` +
		`<div class="result"><a class="result__a" href="https://example.com/x">` +
		`<h2 class="result__title">T</h2><p class="result__snippet">S</p></a></div>` +
		`</body></html>`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(htmlBody))
	}))
	defer ts.Close()

	origBase := ScrapeBase()
	SetScrapeBase(ts.URL)
	defer SetScrapeBase(origBase)

	re, ok := New().(engine.RawEngine)
	if !ok {
		t.Fatal("ddg must implement engine.RawEngine")
	}
	results, raw, err := re.SearchWithRaw(context.Background(), "q", engine.Options{Limit: 10})
	if err != nil {
		t.Fatalf("SearchWithRaw: %v", err)
	}
	if len(results) != 1 || results[0].Title != "T" {
		t.Errorf("results = %+v, want 1 result Title=T", results)
	}
	body, ok := raw.(string)
	if !ok {
		t.Fatalf("raw payload = %T, want string (HTML body)", raw)
	}
	if !strings.Contains(body, `<div class="result">`) {
		t.Errorf("raw HTML body does not contain the fixture markup: %q", body)
	}
}
