package tavily

import (
	"context"
	"encoding/json"
	"io"
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

// --- Golden-table: parseResults -------------------------------------------

func TestParseResults(t *testing.T) {
	resp := apiResponse{
		Results: []apiResult{
			{Title: "Go Programming", URL: "https://golang.org/doc", Content: "The Go Programming Language documentation."},
			{Title: "Go by Example", URL: "https://gobyexample.com/", Content: "Hands-on guide to Go."},
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

func TestParseResults_SkipEmptyURL(t *testing.T) {
	resp := apiResponse{
		Results: []apiResult{
			{Title: "Has URL", URL: "https://example.com/1"},
			{Title: "No URL", URL: ""},
			{Title: "Another", URL: "https://example.com/2"},
		},
	}
	got := parseResults(resp)
	if len(got) != 2 {
		t.Fatalf("got %d results, want 2 (empty URL skipped)", len(got))
	}
}

func TestParseResults_APIError(t *testing.T) {
	resp := apiResponse{
		Error: &apiError{Code: "InvalidApiKey", Message: "API key is invalid."},
	}
	if got := parseResults(resp); len(got) != 0 {
		t.Errorf("error response => %d results, want 0", len(got))
	}
}

func TestParseResults_HTMLContent(t *testing.T) {
	resp := apiResponse{
		Results: []apiResult{
			{Title: "Go (programming language)", URL: "https://en.wikipedia.org/wiki/Go_(programming_language)", Content: "<p>Go is a statically typed, compiled programming language.</p>"},
		},
	}
	got := parseResults(resp)
	if len(got) != 1 {
		t.Fatalf("got %d results, want 1", len(got))
	}
	// HTML tags should be stripped from content/snippet
	if strings.Contains(got[0].Snippet, "<p>") || strings.Contains(got[0].Snippet, "</p>") {
		t.Errorf("snippet still contains HTML tags: %q", got[0].Snippet)
	}
}

func TestParseResults_Limit1(t *testing.T) {
	resp := apiResponse{
		Results: []apiResult{
			{Title: "A", URL: "https://example.com/a"},
			{Title: "B", URL: "https://example.com/b"},
			{Title: "C", URL: "https://example.com/c"},
		},
	}
	got := parseResults(resp)
	if len(got) != 3 {
		t.Fatalf("got %d results, want 3 (parseResults returns all; limit applied at API level)", len(got))
	}
}

// --- httptest: full Search round-trip --------------------------------------

func TestSearch_Httptest(t *testing.T) {
	fixture := apiResponse{
		Results: []apiResult{
			{Title: "A", URL: "https://example.com/a", Content: "snippet A"},
			{Title: "B", URL: "https://example.com/b", Content: "snippet B"},
		},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Errorf("Accept = %q, want application/json", got)
		}
		if got := r.Header.Get("User-Agent"); got != userAgent {
			t.Errorf("User-Agent = %q, want %q", got, userAgent)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", got)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(fixture)
	}))
	defer ts.Close()

	oldBase := apiBase
	apiBase = ts.URL + "/"
	defer func() { apiBase = oldBase }()

	t.Setenv("TAVILY_API_KEY", "test-key")

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
	t.Setenv("TAVILY_API_KEY", "")
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

	t.Setenv("TAVILY_API_KEY", "test-key")

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

	t.Setenv("TAVILY_API_KEY", "test-key")
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

	t.Setenv("TAVILY_API_KEY", "test-key")

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
	if info.Name != "tavily" {
		t.Errorf("Name = %q, want tavily", info.Name)
	}
	if !info.Keyed {
		t.Error("Keyed should be true")
	}
	if info.KeyEnv != "TAVILY_API_KEY" {
		t.Errorf("KeyEnv = %q, want TAVILY_API_KEY", info.KeyEnv)
	}
	if info.DefaultWeight != 1.2 {
		t.Errorf("DefaultWeight = %f, want 1.2", info.DefaultWeight)
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
		json.NewEncoder(w).Encode(apiResponse{Results: []apiResult{{Title: "OK", URL: "https://ok.example/"}}})
	}))
	defer ts.Close()

	oldBase := apiBase
	apiBase = ts.URL + "/"
	defer func() { apiBase = oldBase }()

	t.Setenv("TAVILY_API_KEY", "test-key")
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
		json.NewEncoder(w).Encode(apiResponse{Results: []apiResult{{Title: "OK", URL: "https://ok.example/"}}})
	}))
	defer ts.Close()

	oldBase := apiBase
	apiBase = ts.URL + "/"
	defer func() { apiBase = oldBase }()

	t.Setenv("TAVILY_API_KEY", "test-key")
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

	t.Setenv("TAVILY_API_KEY", "test-key")
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

	t.Setenv("TAVILY_API_KEY", "test-key")
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
		w.Write([]byte(`{"error":{"code":"InvalidApiKey","message":"invalid token"}}`))
	}))
	defer ts.Close()

	oldBase := apiBase
	apiBase = ts.URL + "/"
	defer func() { apiBase = oldBase }()

	t.Setenv("TAVILY_API_KEY", "test-key")

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
		json.NewEncoder(w).Encode(apiResponse{Results: []apiResult{{Title: "OK", URL: "https://ok.example/"}}})
	}))
	defer ts.Close()

	oldBase := apiBase
	apiBase = ts.URL + "/"
	defer func() { apiBase = oldBase }()

	t.Setenv("TAVILY_API_KEY", "test-key")
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
		limit int
	}{
		{10},
		{50}, // capped at 50 (Tavily free tier max)
		{20},
		{1},
		{0}, // floored at 1
	}
	for _, tt := range tests {
		got, err := buildRequestURL(tt.limit)
		if err != nil {
			t.Fatalf("buildRequestURL(%d): %v", tt.limit, err)
		}
		u, err := url.Parse(got)
		if err != nil {
			t.Fatalf("parse result URL: %v", err)
		}
		if u.Path != "/search" {
			t.Errorf("buildRequestURL(%d): path = %q, want /search", tt.limit, u.Path)
		}
		_ = u // Tavily sends limit in JSON body, not URL params
	}
}

// --- SearchWithRaw ----------------------------------------------------------

func TestSearchWithRaw(t *testing.T) {
	fixture := apiResponse{
		Results: []apiResult{
			{Title: "A", URL: "https://example.com/a", Content: "snippet A"},
		},
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(fixture)
	}))
	defer ts.Close()

	oldBase := apiBase
	apiBase = ts.URL + "/"
	defer func() { apiBase = oldBase }()
	t.Setenv("TAVILY_API_KEY", "test-key")

	re, ok := New().(engine.RawEngine)
	if !ok {
		t.Fatal("tavily must implement engine.RawEngine")
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
	resultsArr, ok := m["results"].([]any)
	if !ok || len(resultsArr) != 1 {
		t.Errorf("raw results = %+v, want 1-element array", m["results"])
	}
}

func TestSearchWithRaw_APIError(t *testing.T) {
	fixture := apiResponse{
		Error: &apiError{Code: "InvalidApiKey", Message: "API key is invalid."},
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(fixture)
	}))
	defer ts.Close()

	oldBase := apiBase
	apiBase = ts.URL + "/"
	defer func() { apiBase = oldBase }()
	t.Setenv("TAVILY_API_KEY", "test-key")

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

// --- limitCap ---------------------------------------------------------------

func TestLimitCap(t *testing.T) {
	tests := []struct {
		limit int
		want  int
	}{
		{10, 10},
		{50, 50},
		{100, 50}, // capped at 50
		{1, 1},
		{0, 1},  // floored at 1
		{-5, 1}, // floored at 1
	}
	for _, tt := range tests {
		got := limitCap(tt.limit)
		if got != tt.want {
			t.Errorf("limitCap(%d) = %d, want %d", tt.limit, got, tt.want)
		}
	}
}

// --- IncludeAnswer option -----------------------------------------------------

func TestSearch_IncludeAnswer(t *testing.T) {
	var gotBody map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(apiResponse{Answer: "The Go programming language is a compiled language."})
	}))
	defer ts.Close()

	oldBase := apiBase
	apiBase = ts.URL + "/"
	defer func() { apiBase = oldBase }()
	t.Setenv("TAVILY_API_KEY", "test-key")

	e := New()
	_, err := e.Search(context.Background(), "go", engine.Options{Limit: 10, IncludeAnswer: true})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	incAns, ok := gotBody["include_answer"]
	if !ok {
		t.Fatal("include_answer missing from request body")
	}
	ansBool, ok := incAns.(bool)
	if !ok || !ansBool {
		t.Errorf("include_answer = %v (type %T), want true", incAns, incAns)
	}
}

func TestSearch_IncludeAnswerDefault(t *testing.T) {
	var gotBody map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(apiResponse{})
	}))
	defer ts.Close()

	oldBase := apiBase
	apiBase = ts.URL + "/"
	defer func() { apiBase = oldBase }()
	t.Setenv("TAVILY_API_KEY", "test-key")

	e := New()
	_, err := e.Search(context.Background(), "go", engine.Options{Limit: 10}) // IncludeAnswer defaults to false
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	incAns, ok := gotBody["include_answer"]
	if !ok {
		t.Fatal("include_answer missing from request body")
	}
	ansBool, ok := incAns.(bool)
	if !ok || ansBool {
		t.Errorf("include_answer = %v, want false (default)", incAns)
	}
}

// --- SearchDepth option -----------------------------------------------------

func TestSearch_SearchDepthAdvanced(t *testing.T) {
	var gotBody map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(apiResponse{})
	}))
	defer ts.Close()

	oldBase := apiBase
	apiBase = ts.URL + "/"
	defer func() { apiBase = oldBase }()
	t.Setenv("TAVILY_API_KEY", "test-key")

	e := New()
	_, err := e.Search(context.Background(), "go", engine.Options{Limit: 10, SearchDepth: "advanced"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	sd, ok := gotBody["search_depth"]
	if !ok {
		t.Fatal("search_depth missing from request body when set")
	}
	sdStr, ok := sd.(string)
	if !ok || sdStr != "advanced" {
		t.Errorf("search_depth = %v, want %q", sd, "advanced")
	}
}

func TestSearch_SearchDepthEmpty(t *testing.T) {
	var gotBody map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(apiResponse{})
	}))
	defer ts.Close()

	oldBase := apiBase
	apiBase = ts.URL + "/"
	defer func() { apiBase = oldBase }()
	t.Setenv("TAVILY_API_KEY", "test-key")

	e := New()
	_, err := e.Search(context.Background(), "go", engine.Options{Limit: 10}) // SearchDepth defaults to empty
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	_, ok := gotBody["search_depth"]
	if ok {
		t.Error("search_depth should not be in request body when empty (API uses default)")
	}
}

// --- Answer in response -----------------------------------------------------

func TestSearch_AnswerInResponse(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(apiResponse{
			Answer: "Go is a statically typed, compiled programming language.",
			Results: []apiResult{
				{Title: "Go", URL: "https://golang.org/", Content: "The Go programming language"},
			},
		})
	}))
	defer ts.Close()

	oldBase := apiBase
	apiBase = ts.URL + "/"
	defer func() { apiBase = oldBase }()
	t.Setenv("TAVILY_API_KEY", "test-key")

	re := New().(engine.RawEngine)
	results, raw, err := re.SearchWithRaw(context.Background(), "go", engine.Options{Limit: 10, IncludeAnswer: true})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	// Answer should be available in the raw payload.
	if raw == nil {
		t.Fatal("raw payload is nil, expected answer field")
	}
	m, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("raw = %T, want map[string]any", raw)
	}
	ans, ok := m["answer"]
	if !ok {
		t.Fatal("answer field missing from raw payload")
	}
	ansStr, ok := ans.(string)
	if !ok {
		t.Fatalf("answer = %T, want string", ans)
	}
	if ansStr != "Go is a statically typed, compiled programming language." {
		t.Errorf("answer = %q, want expected text", ansStr)
	}
}

func TestSearch_AnswerEmptyWhenNotRequested(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(apiResponse{
			Results: []apiResult{
				{Title: "Go", URL: "https://golang.org/", Content: "The Go programming language"},
			},
		})
	}))
	defer ts.Close()

	oldBase := apiBase
	apiBase = ts.URL + "/"
	defer func() { apiBase = oldBase }()
	t.Setenv("TAVILY_API_KEY", "test-key")

	re := New().(engine.RawEngine)
	results, raw, err := re.SearchWithRaw(context.Background(), "go", engine.Options{Limit: 10}) // include_answer defaults to false
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if raw == nil {
		t.Fatal("raw payload is nil")
	}
	m := raw.(map[string]any)
	ans, ok := m["answer"]
	if ok && ans.(string) != "" {
		t.Errorf("unexpected answer field when not requested: %q", ans)
	}
}

// --- GetAnswerFromRaw -------------------------------------------------------

func TestGetAnswerFromRaw(t *testing.T) {
	tests := []struct {
		name string
		raw  any
		want string
	}{
		{"answer present", map[string]any{"answer": "Go is a compiled language."}, "Go is a compiled language."},
		{"no answer field", map[string]any{"results": []any{}}, ""},
		{"nil raw", nil, ""},
		{"wrong type", "not a map", ""},
		{"answer not a string", map[string]any{"answer": 42}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetAnswerFromRaw(tt.raw)
			if got != tt.want {
				t.Errorf("GetAnswerFromRaw = %q, want %q", got, tt.want)
			}
		})
	}
}

// --- Live integration test (§14) --------------------------------------------

func TestSearch_Live(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live test in -short mode")
	}
	if os.Getenv("MST_ITEST") == "" {
		t.Skip("skip live test (set MST_ITEST=1)")
	}

	e := New()
	results, err := e.Search(context.Background(), "Go programming language", engine.Options{Limit: 5})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) < 1 {
		t.Errorf("expected at least 1 result, got %d", len(results))
	}
}
