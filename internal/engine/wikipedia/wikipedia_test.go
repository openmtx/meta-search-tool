package wikipedia

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

// === Descriptor =============================================================

func TestDescriptor(t *testing.T) {
	e := New()
	info := e.Descriptor()
	if info.Name != "wikipedia" {
		t.Errorf("Name = %q, want wikipedia", info.Name)
	}
	if info.Keyed {
		t.Error("Keyed should be false")
	}
	if info.KeyEnv != "" {
		t.Errorf("KeyEnv = %q, want empty", info.KeyEnv)
	}
	if info.DefaultWeight != 1.5 {
		t.Errorf("DefaultWeight = %v, want 1.5", info.DefaultWeight)
	}
	if !info.Enabled {
		t.Error("Enabled should be true")
	}
}

// === Golden-table parse (pure function) =====================================

func TestParseResults(t *testing.T) {
	resp := apiResponse{
		json.RawMessage(`"golang"`),
		json.RawMessage(`["Golang","Go (programming language)"]`),
		json.RawMessage(`["Go programming language","General-purpose programming language"]`),
		json.RawMessage(`["https://en.wikipedia.org/wiki/Golang","https://en.wikipedia.org/wiki/Go_(programming_language)"]`),
	}
	results, err := parseResults(resp)
	if err != nil {
		t.Fatalf("parseResults error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}
	if results[0].Title != "Golang" || results[0].URL != "https://en.wikipedia.org/wiki/Golang" {
		t.Errorf("result[0] = %+v, want title=Golang url=wikipedia.org/wiki/Golang", results[0])
	}
	if results[0].Snippet != "Go programming language" {
		t.Errorf("snippet[0] = %q, want Go programming language", results[0].Snippet)
	}
	if results[1].Title != "Go (programming language)" {
		t.Errorf("result[1] title = %q, want Go (programming language)", results[1].Title)
	}
}

func TestParseResults_Empty(t *testing.T) {
	resp := apiResponse{
		json.RawMessage(`"nope"`),
		json.RawMessage(`[]`),
		json.RawMessage(`[]`),
		json.RawMessage(`[]`),
	}
	results, err := parseResults(resp)
	if err != nil {
		t.Fatalf("parseResults error: %v", err)
	}
	if results == nil {
		t.Fatal("results should be empty slice, not nil")
	}
	if len(results) != 0 {
		t.Errorf("results = %d, want 0", len(results))
	}
}

func TestParseResults_SkipEmptyURL(t *testing.T) {
	resp := apiResponse{
		json.RawMessage(`"test"`),
		json.RawMessage(`["Title1","Title2","Title3"]`),
		json.RawMessage(`["snip","","snip3"]`),
		json.RawMessage(`["https://x/1","","https://x/3"]`),
	}
	results, err := parseResults(resp)
	if err != nil {
		t.Fatalf("parseResults error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2 (skipped empty title and empty URL)", len(results))
	}
	if results[0].URL != "https://x/1" {
		t.Errorf("result[0] url = %q, want https://x/1", results[0].URL)
	}
	if results[1].URL != "https://x/3" {
		t.Errorf("result[1] url = %q, want https://x/3", results[1].URL)
	}
}

func TestParseResults_MismatchedLengths(t *testing.T) {
	// Titles has 2 entries, URLs has 1 — should stop at min length.
	resp := apiResponse{
		json.RawMessage(`"q"`),
		json.RawMessage(`["T1","T2"]`),
		json.RawMessage(`["S1","S2"]`),
		json.RawMessage(`["https://x/1"]`),
	}
	results, err := parseResults(resp)
	if err != nil {
		t.Fatalf("parseResults error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1 (limited by shortest array)", len(results))
	}
}

func TestParseResults_TooFewArrays(t *testing.T) {
	resp := apiResponse{json.RawMessage(`["q","q2"]`), json.RawMessage(`["T"]`)} // only 2 elements, need 4
	results, err := parseResults(resp)
	if err == nil {
		t.Fatal("expected error for too-few-arrays response, got nil")
	}
	if results != nil {
		t.Errorf("results = %v, want nil on error", results)
	}
}

func TestParseResults_MalformedInner(t *testing.T) {
	// Element 1 is a JSON object, not a []string — the inner unmarshal must
	// surface an error rather than silently yielding empty results. Regression
	// guard: previously the per-element Unmarshal errors were discarded, so a
	// schema change flattened to ok:true/count:0 instead of a malformed error.
	resp := apiResponse{
		json.RawMessage(`"q"`),
		json.RawMessage(`{"not":"an array"}`),
		json.RawMessage(`[]`),
		json.RawMessage(`[]`),
	}
	results, err := parseResults(resp)
	if err == nil {
		t.Fatal("expected error decoding malformed titles array")
	}
	if results != nil {
		t.Errorf("results = %v, want nil on malformed inner array", results)
	}
}

// === httptest round-trip ====================================================

func TestSearch_Httptest(t *testing.T) {
	// The Wikipedia API returns [query, [titles], [descriptions], [urls]].
	fixture := apiResponse{
		json.RawMessage(`"test query"`),
		json.RawMessage(`["Wikipedia Article 1","Wikipedia Article 2"]`),
		json.RawMessage(`["Description of article 1.","Description of article 2."]`),
		json.RawMessage(`["https://en.wikipedia.org/wiki/Article_One","https://en.wikipedia.org/wiki/Article_Two"]`),
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request.
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.Header.Get("User-Agent") != engine.UserAgent() {
			t.Errorf("User-Agent = %q, want %q", r.Header.Get("User-Agent"), engine.UserAgent())
		}
		if r.Header.Get("Accept") != "application/json" {
			t.Errorf("Accept = %q, want application/json", r.Header.Get("Accept"))
		}
		// r.URL.Query().Get() returns the decoded value.
		q := r.URL.Query().Get("search")
		if q != "test query" {
			t.Errorf("search param = %q, want test query (decoded)", q)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(fixture)
	}))
	defer ts.Close()

	origBase := APIBase()
	SetAPIBase(ts.URL)
	defer SetAPIBase(origBase)

	e := New()
	results, err := e.Search(context.Background(), "test query", engine.Options{Limit: 10})
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}
	if results[0].Title != "Wikipedia Article 1" {
		t.Errorf("result[0] title = %q, want Wikipedia Article 1", results[0].Title)
	}
	if results[0].URL != "https://en.wikipedia.org/wiki/Article_One" {
		t.Errorf("result[0] url = %q, want https://en.wikipedia.org/wiki/Article_One", results[0].URL)
	}
	if results[0].Snippet != "Description of article 1." {
		t.Errorf("snippet[0] = %q, want Description of article 1.", results[0].Snippet)
	}
}

// === buildRequestURL ========================================================

func TestBuildRequestURL(t *testing.T) {
	tests := []struct {
		name  string
		q     string
		limit int
		wantQ string
		wantL string
	}{
		{"normal", "go programming", 10, "go programming", "10"},
		{"capped at 20", "test", 30, "test", "20"},
		{"floored at 1", "test", 0, "test", "1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			origBase := APIBase()
			SetAPIBase("https://example.com/api.php")
			defer SetAPIBase(origBase)

			s, err := buildRequestURL(tt.q, tt.limit)
			if err != nil {
				t.Fatalf("buildRequestURL error: %v", err)
			}
			u, err := url.Parse(s)
			if err != nil {
				t.Fatalf("parse URL: %v", err)
			}
			if u.Query().Get("search") != tt.wantQ {
				t.Errorf("search = %q, want %q", u.Query().Get("search"), tt.wantQ)
			}
			if u.Query().Get("limit") != tt.wantL {
				t.Errorf("limit = %q, want %q", u.Query().Get("limit"), tt.wantL)
			}
			if u.Query().Get("action") != "opensearch" {
				t.Errorf("action = %q, want opensearch", u.Query().Get("action"))
			}
			if u.Query().Get("format") != "json" {
				t.Errorf("format = %q, want json", u.Query().Get("format"))
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

	origBase := APIBase()
	SetAPIBase(ts.URL)
	defer SetAPIBase(origBase)

	e := New()
	results, err := e.Search(context.Background(), "test", engine.Options{Limit: 10})
	if err == nil {
		t.Fatal("expected error on HTTP 404")
	}
	if results != nil {
		t.Error("expected nil results on HTTP 404")
	}
	if !strings.Contains(err.Error(), "wikipedia") {
		t.Errorf("error should mention wikipedia: %v", err)
	}
}

func TestSearch_MalformedResponse(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{not valid json`))
	}))
	defer ts.Close()

	origBase := APIBase()
	SetAPIBase(ts.URL)
	defer SetAPIBase(origBase)

	e := New()
	results, err := e.Search(context.Background(), "test", engine.Options{Limit: 10})
	if err == nil {
		t.Fatal("expected error on malformed JSON")
	}
	if results != nil {
		t.Error("expected nil results on malformed JSON")
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
		w.Header().Set("Content-Type", "application/json")
		// Send raw JSON matching the Wikipedia API shape [query, [titles], [descs], [urls]].
		w.Write([]byte(`["q",["Result 1"],["Snippet 1"],["https://en.wikipedia.org/wiki/Result"]]`))
	}))
	defer ts.Close()

	origBase := APIBase()
	SetAPIBase(ts.URL)
	defer SetAPIBase(origBase)
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

	origBase := APIBase()
	SetAPIBase(ts.URL)
	defer SetAPIBase(origBase)
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

func TestSearch_Retry_5xxSucceedsOnRetry(t *testing.T) {
	attempts := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		// Send raw JSON matching the Wikipedia API shape [query, [titles], [descs], [urls]].
		w.Write([]byte(`["q",["Result 1"],["Snippet 1"],["https://en.wikipedia.org/wiki/Result"]]`))
	}))
	defer ts.Close()

	origBase := APIBase()
	SetAPIBase(ts.URL)
	defer SetAPIBase(origBase)
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

func TestSearch_Retry_ContextCancelledDuringBackoff(t *testing.T) {
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

	origBase := APIBase()
	SetAPIBase(ts.URL)
	defer SetAPIBase(origBase)
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

// TestSearchWithRaw verifies wikipedia implements engine.RawEngine and that the
// raw payload is the decoded native OpenSearch array [query,[titles],[descs],[urls]].
func TestSearchWithRaw(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`["some query",["Article Title"],["A description."],["https://en.wikipedia.org/wiki/Article"]]`))
	}))
	defer ts.Close()

	origBase := apiBase
	apiBase = ts.URL
	defer func() { apiBase = origBase }()

	re, ok := New().(engine.RawEngine)
	if !ok {
		t.Fatal("wikipedia must implement engine.RawEngine")
	}
	results, raw, err := re.SearchWithRaw(context.Background(), "q", engine.Options{Limit: 10})
	if err != nil {
		t.Fatalf("SearchWithRaw: %v", err)
	}
	if len(results) != 1 || results[0].Title != "Article Title" {
		t.Errorf("results = %+v, want 1 result", results)
	}
	// raw is the native [query,[titles],[descs],[urls]] array.
	arr, ok := raw.([]any)
	if !ok || len(arr) != 4 {
		t.Fatalf("raw payload = %+v (%T), want 4-element array", raw, raw)
	}
	if titles, ok := arr[1].([]any); !ok || len(titles) != 1 {
		t.Errorf("raw titles = %+v, want 1-element array", arr[1])
	}
}
