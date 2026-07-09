package ddg

import (
	"context"
	"errors"
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

// fastRetry makes the §11 backoff instant + deterministic for retry tests,
// restoring the original hooks on cleanup. Without this the retry tests would
// sleep the real exponential backoff (~1.5–3s each).
func fastRetry(t *testing.T) {
	t.Helper()
	retry.SetBackoffOverride(func(int) time.Duration { return 0 })
	retry.SetJitterSource(func(time.Duration) time.Duration { return 0 })
}

// ddgResultsHTML builds a #links block of web-result entries matching the real
// DDG no-JS markup SearXNG targets (docs/DDG-research.md).
func ddgResultsHTML(entries ...string) string {
	return `<html><body><div id="links">` + strings.Join(entries, "") + `</div></body></html>`
}

// webResult is a single DDG result entry. The title link is h2 > a (the a holds
// both href and title text); the snippet is a.result__snippet.
func webResult(href, title, snippet string) string {
	return `<div class="web-result">` +
		`<h2><a class="result__a" href="` + href + `">` + title + `</a></h2>` +
		`<a class="result__snippet">` + snippet + `</a>` +
		`</div>`
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
	html := ddgResultsHTML(
		webResult("https://example.com/article1", "Article One", "This is the first article snippet."),
		webResult("https://example.com/article2", "Article Two", "This is the second article snippet."),
	)
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
	// A web-result with an empty href is skipped.
	html := ddgResultsHTML(webResult("", "No Link", "snippet"))
	results, err := parseResults(html)
	if err != nil {
		t.Fatalf("parseResults error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("results = %d, want 0 (empty href)", len(results))
	}
}

func TestParseResults_NoResults(t *testing.T) {
	html := `<html><body><div class="no-results">No results found</div></body></html>`
	results, err := parseResults(html)
	if err != nil {
		t.Fatalf("parseResults error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("results = %d, want 0 (no result divs)", len(results))
	}
}

// TestParseResults_DDGRedirect verifies that uddg= redirect-wrapped hrefs are
// unwrapped to the real destination URL (SearXNG/real DDG wraps results in
// //duckduckgo.com/l/?uddg=<enc> in some templates).
func TestParseResults_DDGRedirect(t *testing.T) {
	wrapped := "//duckduckgo.com/l/?uddg=" + url.QueryEscape("https://example.com/real") + "&rut=abc"
	html := ddgResultsHTML(webResult(wrapped, "Real Title", "real snippet"))
	results, err := parseResults(html)
	if err != nil {
		t.Fatalf("parseResults error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	if results[0].URL != "https://example.com/real" {
		t.Errorf("URL = %q, want https://example.com/real (uddg unwrapped)", results[0].URL)
	}
	if results[0].Title != "Real Title" {
		t.Errorf("title = %q, want Real Title", results[0].Title)
	}
}

// TestParseResults_Captcha verifies the embedded DDG bot-detection challenge
// form is detected and surfaced as ErrCaptcha instead of zero silent results.
func TestParseResults_Captcha(t *testing.T) {
	html := `<html><body>` +
		`<form id="challenge-form" action="/captcha"><input name="cf"></form>` +
		`</body></html>`
	results, err := parseResults(html)
	if !errors.Is(err, ErrCaptcha) {
		t.Fatalf("err = %v, want ErrCaptcha", err)
	}
	if results != nil {
		t.Errorf("results = %v, want nil on CAPTCHA", results)
	}
}

// === httptest round-trip ====================================================

// TestSearch_Httptest verifies the request mechanics (POST, form body, browser
// headers per docs/DDG-research.md) and result parsing end to end.
func TestSearch_Httptest(t *testing.T) {
	fixture := ddgResultsHTML(
		webResult("https://en.wikipedia.org/wiki/Golang", "Golang", "Go programming language."),
		webResult("https://go.dev/doc/", "Go Documentation", "Official Go documentation."),
	)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request mechanics.
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/x-www-form-urlencoded") {
			t.Errorf("Content-Type = %q, want application/x-www-form-urlencoded", ct)
		}
		if r.Header.Get("User-Agent") != ddgUA {
			t.Errorf("User-Agent = %q, want %q", r.Header.Get("User-Agent"), ddgUA)
		}
		// Browser headers DDG's bot blocker expects (docs/DDG-research.md).
		if got := r.Header.Get("Sec-Fetch-Mode"); got != "navigate" {
			t.Errorf("Sec-Fetch-Mode = %q, want navigate", got)
		}
		if got := r.Header.Get("Sec-Fetch-Site"); got != "same-origin" {
			t.Errorf("Sec-Fetch-Site = %q, want same-origin", got)
		}
		if got := r.Header.Get("Referer"); got != "https://html.duckduckgo.com/html/" {
			t.Errorf("Referer = %q, want https://html.duckduckgo.com/html/", got)
		}
		// Form body: q, b (empty), kl (wt-wt). FormValue decodes the POST body.
		if q := r.FormValue("q"); q != "test query" {
			t.Errorf("q = %q, want test query", q)
		}
		if b := r.FormValue("b"); b != "" {
			t.Errorf("b = %q, want empty", b)
		}
		if kl := r.FormValue("kl"); kl != "wt-wt" {
			t.Errorf("kl = %q, want wt-wt", kl)
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

// TestSearch_CaptchaSurfaces verifies a CAPTCHA body is surfaced as ErrCaptcha
// (not silent zero results) after a successful HTTP 200 fetch.
func TestSearch_CaptchaSurfaces(t *testing.T) {
	fixture := `<html><body><form id="challenge-form" action="/captcha"></form></body></html>`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(fixture))
	}))
	defer ts.Close()

	origBase := ScrapeBase()
	SetScrapeBase(ts.URL)
	defer SetScrapeBase(origBase)

	e := New()
	results, err := e.Search(context.Background(), "test", engine.Options{Limit: 10})
	if !errors.Is(err, ErrCaptcha) {
		t.Fatalf("err = %v, want ErrCaptcha", err)
	}
	if results != nil {
		t.Errorf("results = %v, want nil on CAPTCHA", results)
	}
}

// TestSearch_QueryTooLong verifies the ≥500-char guard skips the request
// (matches SearXNG: DDG rejects long queries; we skip with no results/no error).
func TestSearch_QueryTooLong(t *testing.T) {
	hits := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.Write([]byte(ddgResultsHTML(webResult("https://example.com/x", "x", "y"))))
	}))
	defer ts.Close()

	origBase := ScrapeBase()
	SetScrapeBase(ts.URL)
	defer SetScrapeBase(origBase)

	e := New()
	results, err := e.Search(context.Background(), strings.Repeat("a", 500), engine.Options{Limit: 10})
	if err != nil {
		t.Fatalf("err = %v, want nil (skip, not error)", err)
	}
	if results != nil {
		t.Errorf("results = %v, want nil (query too long → skip)", results)
	}
	if hits != 0 {
		t.Errorf("server got %d requests, want 0 (query too long must not be sent)", hits)
	}
}

// === buildRequestBody =======================================================

func TestBuildRequestBody(t *testing.T) {
	tests := []struct {
		name string
		q    string
	}{
		{"normal", "go programming"},
		{"spaces and special", "hello world & <special>"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enc := buildRequestBody(tt.q)
			v, err := url.ParseQuery(enc)
			if err != nil {
				t.Fatalf("ParseQuery: %v", err)
			}
			if v.Get("q") != tt.q {
				t.Errorf("q = %q, want %q", v.Get("q"), tt.q)
			}
			if v.Get("b") != "" {
				t.Errorf("b = %q, want empty", v.Get("b"))
			}
			if v.Get("kl") != "wt-wt" {
				t.Errorf("kl = %q, want wt-wt", v.Get("kl"))
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
		w.Write([]byte(ddgResultsHTML(webResult("https://example.com/1", "Result 1", "Snippet."))))
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
		w.Write([]byte(ddgResultsHTML(webResult("https://example.com/1", "Result 1", "Snippet."))))
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
	htmlBody := ddgResultsHTML(webResult("https://example.com/x", "T", "S"))
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
	if !strings.Contains(body, `class="web-result"`) {
		t.Errorf("raw HTML body does not contain the fixture markup: %q", body)
	}
}

// === Live integration test (§14, MST_ITEST=1) ===============================

// TestSearch_Live verifies the engine against the real html.duckduckgo.com
// endpoint — confirming the request mechanics and selectors still match live
// markup. Skipped unless MST_ITEST=1. This is the gate the existing DECISIONS
// entry requires before promoting ddg toward default-on.
func TestSearch_Live(t *testing.T) {
	if os.Getenv("MST_ITEST") != "1" {
		t.Skip("skipping live test; set MST_ITEST=1 to hit html.duckduckgo.com")
	}
	e := New()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	results, err := e.Search(ctx, "go programming language", engine.Options{Limit: 20})
	if err != nil {
		t.Fatalf("live ddg search failed (selectors may have drifted or IP blocked): %v", err)
	}
	if len(results) == 0 {
		t.Fatal("live ddg search returned 0 results (selectors may have drifted or IP is blocked)")
	}
	t.Logf("ddg live returned %d results; first: %q <%s> — %s",
		len(results), results[0].Title, results[0].URL, results[0].Snippet)

	// Sanity: result URLs must not be left as duckduckgo.com redirect links
	// (would indicate uddg= unwrapping is broken).
	n := len(results)
	if n > 5 {
		n = 5
	}
	for _, r := range results[:n] {
		if strings.Contains(r.URL, "duckduckgo.com/l/") || strings.Contains(r.URL, "uddg=") {
			t.Errorf("result URL not unwrapped from DDG redirect: %s", r.URL)
		}
		if r.Title == "" {
			t.Errorf("result has empty title: %+v", r)
		}
	}
}
