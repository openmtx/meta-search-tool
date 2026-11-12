package bing

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

// fastRetry makes the §11 backoff instant + deterministic for retry tests,
// restoring the original hooks on cleanup. Without this the retry tests would
// sleep the real exponential backoff (~1.5–3s each).
func fastRetry(t *testing.T) {
	t.Helper()
	retry.SetBackoffOverride(func(int) time.Duration { return 0 })
	retry.SetJitterSource(func(time.Duration) time.Duration { return 0 })
}

// --- Golden-table: parseResults -------------------------------------------

func TestParseResults(t *testing.T) {
	resp := apiResponse{
		WebPages: &webPages{
			Results: []apiResult{
				{DisplayName: "Go Programming", URL: "https://golang.org/doc", Snippet: "The Go Programming Language documentation."},
				{DisplayName: "Go by Example", URL: "https://gobyexample.com/", Snippet: "Hands-on guide to Go."},
			},
		},
	}
	got := parseResults(resp)
	if len(got) != 2 {
		t.Fatalf("got %d results, want 2", len(got))
	}
	if got[0].Title != "Go Programming" || got[0].URL != "https://golang.org/doc" {
		t.Errorf("result[0] = %+v, want Title=Go Programming URL=https://golang.org/doc", got[0])
	}
	if got[1].Title != "Go by Example" || got[1].URL != "https://gobyexample.com/" {
		t.Errorf("result[1] = %+v, want Title=Go by Example URL=https://gobyexample.com/", got[1])
	}
}

func TestParseResults_Empty(t *testing.T) {
	if got := parseResults(apiResponse{}); len(got) != 0 {
		t.Errorf("empty response => %d results, want 0", len(got))
	}
}

func TestParseResults_NilWebPages(t *testing.T) {
	resp := apiResponse{
		WebPages: nil,
	}
	if got := parseResults(resp); len(got) != 0 {
		t.Errorf("nil webPages => %d results, want 0", len(got))
	}
}

func TestParseResults_SkipEmptyURL(t *testing.T) {
	resp := apiResponse{
		WebPages: &webPages{
			Results: []apiResult{
				{DisplayName: "Has URL", URL: "https://example.com/1"},
				{DisplayName: "No URL", URL: ""},
				{DisplayName: "Another", URL: "https://example.com/2"},
			},
		},
	}
	got := parseResults(resp)
	if len(got) != 2 {
		t.Fatalf("got %d results, want 2 (empty URL skipped)", len(got))
	}
}

func TestParseResults_APIError(t *testing.T) {
	resp := apiResponse{
		Error: &apiError{Code: "CredentialsNotFound", Message: "Credentials are required for this request."},
	}
	if got := parseResults(resp); len(got) != 0 {
		t.Errorf("error response => %d results, want 0", len(got))
	}
}

// --- httptest: full Search round-trip --------------------------------------

func TestSearch_Httptest(t *testing.T) {
	fixture := apiResponse{
		WebPages: &webPages{
			Results: []apiResult{
				{DisplayName: "A", URL: "https://example.com/a", Snippet: "snippet A"},
				{DisplayName: "B", URL: "https://example.com/b", Snippet: "snippet B"},
			},
		},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Errorf("Accept = %q, want application/json", got)
		}
		if got := r.Header.Get("User-Agent"); got != engine.UserAgent() {
			t.Errorf("User-Agent = %q, want %q", got, engine.UserAgent())
		}
		if got := r.Header.Get("Ocp-Apim-Subscription-Key"); got == "" {
			t.Error("Ocp-Apim-Subscription-Key header missing")
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(fixture)
	}))
	defer ts.Close()

	oldBase := apiBase
	apiBase = ts.URL + "/"
	defer func() { apiBase = oldBase }()

	t.Setenv("BING_API_KEY", "test-key")

	e := New()
	results, err := e.Search(context.Background(), "go programming", engine.Options{Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	if results[0].Title != "A" || results[0].Snippet != "snippet A" {
		t.Errorf("result[0] = %+v, want Title=A Snippet=snippet A", results[0])
	}
	if results[1].Title != "B" || results[1].Snippet != "snippet B" {
		t.Errorf("result[1] = %+v, want Title=B Snippet=snippet B", results[1])
	}
}

func TestSearch_NoKey(t *testing.T) {
	t.Setenv("BING_API_KEY", "")
	e := New()
	results, err := e.Search(context.Background(), "q", engine.Options{Limit: 10})
	if err == nil {
		t.Fatal("expected error with no API key")
	}
	if results != nil {
		t.Errorf("expected nil results on error, got %d", len(results))
	}
}

func TestSearch_ContextCancelled(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	oldBase := apiBase
	apiBase = ts.URL + "/"
	defer func() { apiBase = oldBase }()

	t.Setenv("BING_API_KEY", "test-key")

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
	case <-time.After(2 * time.Second):
		t.Fatal("Search did not return after context cancel")
	}
}

func TestSearch_HTTPError(t *testing.T) {
	var count int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":{"code":"429","message":"rate limit"}}`))
	}))
	defer ts.Close()

	oldBase := apiBase
	apiBase = ts.URL + "/"
	defer func() { apiBase = oldBase }()

	t.Setenv("BING_API_KEY", "test-key")
	fastRetry(t)

	e := New()
	results, err := e.Search(context.Background(), "q", engine.Options{Limit: 10})
	if err == nil {
		t.Fatal("expected error on HTTP 429 after retries")
	}
	if results != nil {
		t.Errorf("expected nil results on error, got %d", len(results))
	}
	if count != 3 {
		t.Errorf("HTTP request count = %d, want 3 (1 initial + 2 retries)", count)
	}
}

func TestSearch_MalformedResponse(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`not json`))
	}))
	defer ts.Close()

	oldBase := apiBase
	apiBase = ts.URL + "/"
	defer func() { apiBase = oldBase }()

	t.Setenv("BING_API_KEY", "test-key")

	e := New()
	results, err := e.Search(context.Background(), "q", engine.Options{Limit: 10})
	if err == nil {
		t.Fatal("expected error on malformed response")
	}
	if results != nil {
		t.Errorf("expected nil results on error, got %d", len(results))
	}
}

// --- Descriptor -------------------------------------------------------------

func TestDescriptor(t *testing.T) {
	e := New()
	info := e.Descriptor()
	if info.Name != "bing" {
		t.Errorf("Name = %q, want bing", info.Name)
	}
	if !info.Keyed {
		t.Error("Keyed should be true")
	}
	if info.KeyEnv != "BING_API_KEY" {
		t.Errorf("KeyEnv = %q, want BING_API_KEY", info.KeyEnv)
	}
	if info.DefaultWeight != 1.0 {
		t.Errorf("DefaultWeight = %f, want 1.0", info.DefaultWeight)
	}
	if info.Enabled {
		t.Error("Enabled should be false")
	}
	if info.Category != "general" {
		t.Errorf("Category = %q, want general", info.Category)
	}
}

// --- Retry: HTTP 429/5xx backoff -------------------------------------------

func TestSearch_Retry_429SucceedsOnRetry(t *testing.T) {
	var count int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		if count == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(apiResponse{WebPages: &webPages{Results: []apiResult{{DisplayName: "OK", URL: "https://ok.example/"}}}})
	}))
	defer ts.Close()

	oldBase := apiBase
	apiBase = ts.URL + "/"
	defer func() { apiBase = oldBase }()

	t.Setenv("BING_API_KEY", "test-key")
	fastRetry(t)

	e := New()
	results, err := e.Search(context.Background(), "q", engine.Options{Limit: 10})
	if err != nil {
		t.Fatalf("expected success after retry: %v", err)
	}
	if len(results) != 1 || results[0].Title != "OK" {
		t.Errorf("unexpected results: %+v", results)
	}
	if count != 2 {
		t.Errorf("HTTP request count = %d, want 2", count)
	}
}

func TestSearch_Retry_5xxSucceedsOnRetry(t *testing.T) {
	var count int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		if count == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(apiResponse{WebPages: &webPages{Results: []apiResult{{DisplayName: "OK", URL: "https://ok.example/"}}}})
	}))
	defer ts.Close()

	oldBase := apiBase
	apiBase = ts.URL + "/"
	defer func() { apiBase = oldBase }()

	t.Setenv("BING_API_KEY", "test-key")
	fastRetry(t)

	e := New()
	results, err := e.Search(context.Background(), "q", engine.Options{Limit: 10})
	if err != nil {
		t.Fatalf("expected success after retry: %v", err)
	}
	if len(results) != 1 || results[0].Title != "OK" {
		t.Errorf("unexpected results: %+v", results)
	}
	if count != 2 {
		t.Errorf("HTTP request count = %d, want 2", count)
	}
}

func TestSearch_Retry_Exhausted(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer ts.Close()

	oldBase := apiBase
	apiBase = ts.URL + "/"
	defer func() { apiBase = oldBase }()

	t.Setenv("BING_API_KEY", "test-key")
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

func TestSearch_Retry_ContextCancelledDuringBackoff(t *testing.T) {
	var count int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer ts.Close()

	oldBase := apiBase
	apiBase = ts.URL + "/"
	defer func() { apiBase = oldBase }()

	t.Setenv("BING_API_KEY", "test-key")
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
	case <-time.After(2 * time.Second):
		t.Fatal("Search did not return after context cancel during backoff")
	}
	if count < 2 {
		t.Errorf("HTTP request count = %d, want >= 2", count)
	}
}

func TestSearch_Retry_NonRetryableStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":{"code":"InvalidCredentials","message":"invalid token"}}`))
	}))
	defer ts.Close()

	oldBase := apiBase
	apiBase = ts.URL + "/"
	defer func() { apiBase = oldBase }()

	t.Setenv("BING_API_KEY", "test-key")

	e := New()
	results, err := e.Search(context.Background(), "q", engine.Options{Limit: 10})
	if err == nil {
		t.Fatal("expected error on HTTP 401")
	}
	if results != nil {
		t.Errorf("expected nil results, got %d", len(results))
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error should mention 401: %v", err)
	}
}

func TestSearch_Retry_RetryAfterHonored(t *testing.T) {
	var count int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		if count == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(apiResponse{WebPages: &webPages{Results: []apiResult{{DisplayName: "OK", URL: "https://ok.example/"}}}})
	}))
	defer ts.Close()

	oldBase := apiBase
	apiBase = ts.URL + "/"
	defer func() { apiBase = oldBase }()

	t.Setenv("BING_API_KEY", "test-key")
	fastRetry(t)

	e := New()
	results, err := e.Search(context.Background(), "q", engine.Options{Limit: 10})
	if err != nil {
		t.Fatalf("expected success after retry: %v", err)
	}
	if len(results) != 1 || results[0].Title != "OK" {
		t.Errorf("unexpected results: %+v", results)
	}
	if count != 2 {
		t.Errorf("HTTP request count = %d, want 2", count)
	}
}

// --- buildRequestURL --------------------------------------------------------

func TestBuildRequestURL(t *testing.T) {
	tests := []struct {
		q       string
		limit   int
		wantCnt string
	}{
		{"go", 10, "10"},
		{"go", 50, "50"}, // capped at 50 (Bing max)
		{"go", 20, "20"},
		{"go", 1, "1"},
		{"go", 0, "1"}, // floored at 1
	}
	for _, tt := range tests {
		got, err := buildRequestURL(tt.q, tt.limit)
		if err != nil {
			t.Fatalf("buildRequestURL(%q, %d): %v", tt.q, tt.limit, err)
		}
		u, err := url.Parse(got)
		if err != nil {
			t.Fatalf("parse result URL: %v", err)
		}
		if u.Query().Get("count") != tt.wantCnt {
			t.Errorf("buildRequestURL(%q, %d): count = %q, want %q", tt.q, tt.limit, u.Query().Get("count"), tt.wantCnt)
		}
		if u.Query().Get("q") != tt.q {
			t.Errorf("buildRequestURL(%q, %d): q = %q, want %q", tt.q, tt.limit, u.Query().Get("q"), tt.q)
		}
	}
}

// TestSearchWithRaw verifies bing implements engine.RawEngine and that the raw
// payload is the decoded native JSON (full structure, not just mapped fields).
func TestSearchWithRaw(t *testing.T) {
	fixture := apiResponse{
		WebPages: &webPages{Results: []apiResult{
			{DisplayName: "A", URL: "https://example.com/a", Snippet: "snippet A"},
		}},
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(fixture)
	}))
	defer ts.Close()

	oldBase := apiBase
	apiBase = ts.URL + "/"
	defer func() { apiBase = oldBase }()
	t.Setenv("BING_API_KEY", "test-key")

	re, ok := New().(engine.RawEngine)
	if !ok {
		t.Fatal("bing must implement engine.RawEngine")
	}
	results, raw, err := re.SearchWithRaw(context.Background(), "q", engine.Options{Limit: 10})
	if err != nil {
		t.Fatalf("SearchWithRaw: %v", err)
	}
	if len(results) != 1 || results[0].Title != "A" {
		t.Errorf("results = %+v, want 1 result Title=A", results)
	}
	m, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("raw payload = %T, want map[string]any", raw)
	}
	webPages, ok := m["webPages"].(map[string]any)
	if !ok {
		t.Fatalf("raw[webPages] = %T, want map[string]any", m["webPages"])
	}
	resultsArr, ok := webPages["value"].([]any)
	if !ok || len(resultsArr) != 1 {
		t.Errorf("raw webPages.value = %+v, want 1-element array", webPages["value"])
	}
}

// TestSearchWithRaw_APIError verifies SearchWithRaw returns raw=nil on API error.
func TestSearchWithRaw_APIError(t *testing.T) {
	fixture := apiResponse{
		Error: &apiError{Code: "Unauthorized", Message: "Invalid credentials."},
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(fixture)
	}))
	defer ts.Close()

	oldBase := apiBase
	apiBase = ts.URL + "/"
	defer func() { apiBase = oldBase }()
	t.Setenv("BING_API_KEY", "test-key")

	re := New().(engine.RawEngine)
	results, raw, err := re.SearchWithRaw(context.Background(), "q", engine.Options{Limit: 10})
	if err == nil {
		t.Fatal("expected error from API error response")
	}
	if results != nil {
		t.Errorf("expected nil results on API error, got %d", len(results))
	}
	if raw != nil {
		t.Error("expected nil raw on API error")
	}
}
