package google

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
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

// ---- Fixture helpers -------------------------------------------------------

// apiFixtureJSON returns a valid Google Custom Search JSON response.
func apiFixtureJSON(items ...string) string {
	if len(items) == 0 {
		return `{"items":[]}`
	}
	return `{"items":[` + strings.Join(items, ",") + `]}`
}

func apiItemJSON(title, link, snippet string) string {
	b, _ := json.Marshal(map[string]string{"title": title, "link": link, "snippet": snippet})
	return string(b)
}

// scrapeResultsHTML builds a Google HTML search page with given results.
func scrapeResultsHTML(entries ...string) string {
	return `<html><body>` + strings.Join(entries, "") + `</body></html>`
}

// scrapeResult is a single Google scrape result link (/url?q=...).
func scrapeResult(href, title, snippet string) string {
	return `<div class="g"><h3><a href="` + href + `">` + title + `</a></h3>` +
		snippet + `</div>`
}

// ---- Descriptor ============================================================

func TestDescriptor(t *testing.T) {
	g := New()
	info := g.Descriptor()
	if info.Name != "google" {
		t.Errorf("Name = %q, want google", info.Name)
	}
	if info.Keyed {
		t.Error("Keyed should be false (engine works without keys via scrape)")
	}
	if info.KeyEnv != "" {
		t.Errorf("KeyEnv = %q, want empty", info.KeyEnv)
	}
	if info.DefaultWeight != 1.0 {
		t.Errorf("DefaultWeight = %v, want 1.0", info.DefaultWeight)
	}
	if info.Enabled {
		t.Error("Enabled should be false (opt-in)")
	}
}

// ---- API path: golden-table parse (pure function) ==========================

func TestParseAPIResults(t *testing.T) {
	resp := apiResponse{
		Items: []apiItem{
			{Title: "Result One", Link: "https://example.com/1", Snippet: "Snippet one."},
			{Title: "Result Two", Link: "https://example.com/2", Snippet: "Snippet two."},
		},
	}
	results := parseAPIResults(resp)
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

func TestParseAPIResults_Empty(t *testing.T) {
	resp := apiResponse{Items: []apiItem{}}
	results := parseAPIResults(resp)
	if len(results) != 0 {
		t.Errorf("results = %d, want 0", len(results))
	}
}

func TestParseAPIResults_SkipEmptyURL(t *testing.T) {
	resp := apiResponse{
		Items: []apiItem{
			{Title: "No Link", Link: "", Snippet: "skip"},
			{Title: "Has Link", Link: "https://example.com/y", Snippet: "keep"},
		},
	}
	results := parseAPIResults(resp)
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	if results[0].Title != "Has Link" {
		t.Errorf("title = %q, want Has Link", results[0].Title)
	}
}

// ---- API path: httptest round-trip =========================================

func TestSearch_API_Httptest(t *testing.T) {
	t.Setenv("GOOGLE_API_KEY", "test-api-key")
	t.Setenv("GOOGLE_CX", "test-cx")

	fixture := apiFixtureJSON(
		apiItemJSON("Go Programming", "https://go.dev/", "The Go programming language."),
		apiItemJSON("Go Wiki", "https://github.com/golang/go/wiki", "Unofficial Go wiki."),
	)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		// Verify API params.
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

// ---- API path: buildRequestURL =============================================

func TestBuildAPIURL(t *testing.T) {
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
			u, err := buildAPIURL(tt.q, tt.key, tt.cx, tt.limit)
			if err != nil {
				t.Fatalf("buildAPIURL error: %v", err)
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

// ---- API path: error paths =================================================

func TestSearch_API_ContextCancelled(t *testing.T) {
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

func TestSearch_API_HTTPError(t *testing.T) {
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

func TestSearch_API_AuthError(t *testing.T) {
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

func TestSearch_API_MalformedResponse(t *testing.T) {
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

// ---- API path: retry tests =================================================

func TestSearch_API_Retry_429SucceedsOnRetry(t *testing.T) {
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
		w.Write([]byte(apiFixtureJSON(apiItemJSON("R1", "https://e.com/1", "S1."))))
	}))
	defer ts.Close()

	origBase := APIBase()
	SetAPIBase(ts.URL)
	defer SetAPIBase(origBase)
	fastRetry(t)

	g := New()
	t.Setenv("GOOGLE_API_KEY", "k")
	t.Setenv("GOOGLE_CX", "cx")
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

func TestSearch_API_Retry_5xxSucceedsOnRetry(t *testing.T) {
	t.Setenv("GOOGLE_API_KEY", "k")
	t.Setenv("GOOGLE_CX", "cx")
	attempts := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Write([]byte(apiFixtureJSON(apiItemJSON("R1", "https://e.com/1", "S1."))))
	}))
	defer ts.Close()

	origBase := APIBase()
	SetAPIBase(ts.URL)
	defer SetAPIBase(origBase)
	fastRetry(t)

	g := New()
	t.Setenv("GOOGLE_API_KEY", "k")
	t.Setenv("GOOGLE_CX", "cx")
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

func TestSearch_API_Retry_Exhausted(t *testing.T) {
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

func TestSearch_API_Retry_ContextCancelledDuringBackoff(t *testing.T) {
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
	case <-time.After(2 * time.Second):
		t.Fatal("Search did not return after context cancel during backoff")
	}
	if attempts < 2 {
		t.Errorf("HTTP request count = %d, want >= 2", attempts)
	}
}

func TestSearch_API_Retry_NonRetryableStatus(t *testing.T) {
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

// ---- API path: SearchWithRaw ===============================================

func TestSearch_API_SearchWithRaw(t *testing.T) {
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

// ============================================================================
// ---- Scrape path (fallback when no API keys) --------------------------------
// ============================================================================

// ---- Scrape: buildScrapeURL -------------------------------------------------

func TestBuildScrapeURL(t *testing.T) {
	tests := []struct {
		name      string
		q         string
		start     int
		wantQ     string
		wantStart string
	}{
		{"page 1 start 0", "go", 0, "go", "0"},
		{"page 2 start 10", "test", 10, "test", "10"},
		{"page 3 start 20", "query with spaces", 20, "query with spaces", "20"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, err := buildScrapeURL(tt.q, tt.start)
			if err != nil {
				t.Fatalf("buildScrapeURL error: %v", err)
			}
			parsed, err := url.Parse(u)
			if err != nil {
				t.Fatalf("failed to parse: %v", err)
			}
			q := parsed.Query()
			if q.Get("q") != tt.wantQ {
				t.Errorf("q = %q, want %q", q.Get("q"), tt.wantQ)
			}
			if q.Get("start") != tt.wantStart {
				t.Errorf("start = %q, want %q", q.Get("start"), tt.wantStart)
			}
			if q.Get("filter") != "0" {
				t.Errorf("filter = %q, want 0", q.Get("filter"))
			}
		})
	}
}

// ---- Scrape: isGoogleSorry --------------------------------------------------

func TestIsGoogleSorry(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{"short body with /sorry/", `<html>/sorry/</html>`, true},
		{"sorry.google.com", `sorry.google.com/sorry?continue`, true},
		{"normal results", `<html><body>search results here</body></html>`, false},
		{"short body without sorry", `<html>hi</html>`, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isGoogleSorry(tt.body)
			if got != tt.want {
				t.Errorf("isGoogleSorry = %v, want %v", got, tt.want)
			}
		})
	}
}

// ---- Scrape: unwrapGoogleURL ------------------------------------------------

func TestUnwrapGoogleURL(t *testing.T) {
	tests := []struct {
		name string
		href string
		want string
	}{
		{"standard /url?q=", "/url?q=https://example.com&sa=U&ved=0", "https://example.com"},
		{"encoded URL", "/url?q=https%3A%2F%2Fexample.com%2Fa+b&sa=U", "https://example.com/a b"},
		{"no prefix (direct)", "https://example.com", "https://example.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := unwrapGoogleURL(tt.href)
			if got != tt.want {
				t.Errorf("unwrapGoogleURL(%q) = %q, want %q", tt.href, got, tt.want)
			}
		})
	}
}

// ---- Scrape: parseScrapeResults ---------------------------------------------

func TestParseScrapeResults(t *testing.T) {
	html := scrapeResultsHTML(
		scrapeResult("/url?q=https://go.dev/&sa=U", "The Go Language", "A compiled language."),
		scrapeResult("/url?q=https://example.com/&sa=U", "Example Site", "An example site."),
	)
	results := parseScrapeResults(html)
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}
	if results[0].URL != "https://go.dev/" {
		t.Errorf("result[0].URL = %q, want https://go.dev/", results[0].URL)
	}
	if results[0].Title != "The Go Language" {
		t.Errorf("result[0].Title = %q, want The Go Language", results[0].Title)
	}
}

func TestParseScrapeResults_Empty(t *testing.T) {
	html := `<html><body>no results</body></html>`
	results := parseScrapeResults(html)
	if len(results) != 0 {
		t.Errorf("results = %d, want 0", len(results))
	}
}

func TestParseScrapeResults_Dedup(t *testing.T) {
	// Two identical URLs — should dedup to 1.
	html := scrapeResultsHTML(
		scrapeResult("/url?q=https://example.com&sa=U", "Title", ""),
		scrapeResult("/url?q=https://example.com&sa=U", "Title", ""),
	)
	results := parseScrapeResults(html)
	if len(results) != 1 {
		t.Errorf("results = %d, want 1 (dedup)", len(results))
	}
}

// ---- Scrape: httptest round-trip -------------------------------------------

func TestSearch_Scrape_Httptest(t *testing.T) {
	fixture := scrapeResultsHTML(
		scrapeResult("/url?q=https://example.com/1&sa=U", "Result 1", "Snippet 1."),
		scrapeResult("/url?q=https://example.com/2&sa=U", "Result 2", "Snippet 2."),
	)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if ua := r.Header.Get("User-Agent"); !strings.Contains(ua, "Chrome") {
			t.Errorf("User-Agent = %q, want a browser UA", ua)
		}
		if cookie := r.Header.Get("Cookie"); !strings.Contains(cookie, "CONSENT=YES+") {
			t.Errorf("Cookie = %q, want CONSENT=YES+", cookie)
		}
		q := r.URL.Query()
		if q.Get("q") != "test query" {
			t.Errorf("q = %q, want test query", q.Get("q"))
		}
		// Page 1 only (start=0). Subsequent pages return empty results.
		if q.Get("start") == "0" {
			w.Write([]byte(fixture))
		} else {
			w.Write([]byte(`<html><body></body></html>`))
		}
	}))
	defer ts.Close()

	origBase := ScrapeBase()
	SetScrapeBase(ts.URL)
	defer SetScrapeBase(origBase)

	g := New()
	results, err := g.Search(context.Background(), "test query", engine.Options{Limit: 10})
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}
	if results[0].Title != "Result 1" {
		t.Errorf("result[0].Title = %q, want Result 1", results[0].Title)
	}
	if results[0].URL != "https://example.com/1" {
		t.Errorf("result[0].URL = %q, want https://example.com/1", results[0].URL)
	}
}

func TestSearch_Scrape_Captcha(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><body>/sorry/</body></html>`))
	}))
	defer ts.Close()

	origBase := ScrapeBase()
	SetScrapeBase(ts.URL)
	defer SetScrapeBase(origBase)

	g := New()
	results, err := g.Search(context.Background(), "test", engine.Options{Limit: 10})
	if err == nil {
		t.Fatal("expected CAPTCHA error")
	}
	if !strings.Contains(err.Error(), "CAPTCHA") {
		t.Errorf("error should mention CAPTCHA: %v", err)
	}
	if results != nil {
		t.Error("expected nil results on CAPTCHA")
	}
}

func TestSearch_Scrape_MultiPage(t *testing.T) {
	var pageCount int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pageCount++
		if pageCount == 1 {
			w.Write([]byte(scrapeResultsHTML(
				scrapeResult("/url?q=https://e.com/1&sa=U", "Page1", ""),
			)))
		} else {
			// Page 2: return 1 result → loop continues (but hits Limit next check)
			w.Write([]byte(scrapeResultsHTML(
				scrapeResult("/url?q=https://e.com/2&sa=U", "Page2", ""),
			)))
		}
	}))
	defer ts.Close()

	origBase := ScrapeBase()
	SetScrapeBase(ts.URL)
	defer SetScrapeBase(origBase)

	g := New()
	results, err := g.Search(context.Background(), "test", engine.Options{Limit: 2})
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if pageCount != 2 {
		t.Errorf("pageCount = %d, want 2", pageCount)
	}
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2 (1 from each page)", len(results))
	}
}

func TestSearch_Scrape_ContextCancelled(t *testing.T) {
	e := New()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	results, err := e.Search(ctx, "test", engine.Options{Limit: 10})
	if err == nil {
		t.Fatal("expected error on cancelled context")
	}
	if results != nil {
		t.Error("expected nil results")
	}
}
