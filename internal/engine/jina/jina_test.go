package jina

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openmtx/meta-search-tool/internal/engine"
	"github.com/openmtx/meta-search-tool/internal/engine/retry"
)

func fastRetry(t *testing.T) {
	t.Helper()
	retry.SetBackoffOverride(func(int) time.Duration { return 0 })
	retry.SetJitterSource(func(time.Duration) time.Duration { return 0 })
}

const fixture = `{"data":[
  {"title":"First","url":"https://example.com/1","description":"first desc","date":"2026-01-01"},
  {"title":"","url":"https://example.com/2","description":"second desc"},
  {"title":"No URL","url":"","description":"skip me"}
]}`

func TestDescriptor(t *testing.T) {
	d := New().Descriptor()
	if d.Name != "jina" || !d.Keyed || d.KeyEnv != "JINA_API_KEY" || d.DefaultWeight != 1.1 || d.Enabled || d.Category != "general" {
		t.Errorf("Descriptor() = %+v", d)
	}
}

func TestBuildRequestURL(t *testing.T) {
	old := apiBase
	defer func() { apiBase = old }()
	apiBase = "https://example.test/"

	got, err := buildRequestURL("golang", 2, "google")
	if err != nil {
		t.Fatalf("buildRequestURL: %v", err)
	}
	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("parse URL: %v", err)
	}
	q := u.Query()
	if q.Get("q") != "golang" || q.Get("page") != "2" || q.Get("engine") != "google" || q.Get("respondWith") != "no-content" {
		t.Errorf("query params = %v", q)
	}
}

func TestParseResults(t *testing.T) {
	var resp apiResponse
	if err := json.Unmarshal([]byte(fixture), &resp); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	got := parseResults(resp.Data)
	if len(got) != 2 {
		t.Fatalf("got %d results, want 2 (empty URL skipped)", len(got))
	}
	if got[0].Title != "First" || got[0].Snippet != "first desc" {
		t.Errorf("result[0] = %+v", got[0])
	}
	if got[1].Title != "https://example.com/2" {
		t.Errorf("empty title should fall back to URL, got %q", got[1].Title)
	}
}

func TestEngineMode(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", "reader"},
		{"reader", "reader"},
		{"google", "google"},
		{"BING", "bing"},
		{"bogus", "reader"},
	}
	for _, tt := range tests {
		if got := engineMode(engine.Options{JinaEngine: tt.in}); got != tt.want {
			t.Errorf("engineMode(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestSearch_Httptest(t *testing.T) {
	t.Setenv("JINA_API_KEY", "test-key")
	old := apiBase
	defer func() { apiBase = old }()

	var calls int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("User-Agent"); got != engine.UserAgent() {
			t.Errorf("User-Agent = %q, want %q", got, engine.UserAgent())
		}
		if r.URL.Query().Get("page") == "1" {
			w.Write([]byte(`{"data":[{"title":"One","url":"https://example.com/1","description":"d1"}]}`))
			return
		}
		w.Write([]byte(`{"data":[]}`))
	}))
	defer ts.Close()
	apiBase = ts.URL

	results, err := New().Search(context.Background(), "q", engine.Options{Limit: 5})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	// Page 1 returns one result (< limit), so page 2 is requested and empty.
	if calls != 2 {
		t.Errorf("calls = %d, want 2", calls)
	}
}

func TestSearch_MissingKey(t *testing.T) {
	t.Setenv("JINA_API_KEY", "")
	if _, err := New().Search(context.Background(), "q", engine.Options{Limit: 5}); err == nil {
		t.Fatal("expected missing-key error")
	}
}

func TestSearch_HTTPError(t *testing.T) {
	t.Setenv("JINA_API_KEY", "test-key")
	old := apiBase
	defer func() { apiBase = old }()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer ts.Close()
	apiBase = ts.URL

	if _, err := New().Search(context.Background(), "q", engine.Options{Limit: 5}); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("error = %v, want HTTP 401", err)
	}
}

func TestSearch_MalformedResponse(t *testing.T) {
	t.Setenv("JINA_API_KEY", "test-key")
	old := apiBase
	defer func() { apiBase = old }()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	}))
	defer ts.Close()
	apiBase = ts.URL

	if _, err := New().Search(context.Background(), "q", engine.Options{Limit: 5}); err == nil {
		t.Fatal("expected decode error")
	}
}

func TestSearch_ContextCancelled(t *testing.T) {
	t.Setenv("JINA_API_KEY", "test-key")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := New().Search(ctx, "q", engine.Options{Limit: 5}); err == nil {
		t.Fatal("expected cancellation error")
	}
}

func TestSearch_Retry429ThenSuccess(t *testing.T) {
	t.Setenv("JINA_API_KEY", "test-key")
	old := apiBase
	defer func() { apiBase = old }()
	fastRetry(t)

	var calls int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Write([]byte(`{"data":[{"title":"One","url":"https://example.com/1"}]}`))
	}))
	defer ts.Close()
	apiBase = ts.URL

	results, err := New().Search(context.Background(), "q", engine.Options{Limit: 1})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 || calls != 2 {
		t.Errorf("results=%d calls=%d, want 1/2", len(results), calls)
	}
}

func TestSearchWithRaw(t *testing.T) {
	t.Setenv("JINA_API_KEY", "test-key")
	old := apiBase
	defer func() { apiBase = old }()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[{"title":"One","url":"https://example.com/1"}]}`))
	}))
	defer ts.Close()
	apiBase = ts.URL

	e := New().(engine.RawEngine)
	results, raw, err := e.SearchWithRaw(context.Background(), "q", engine.Options{Limit: 1})
	if err != nil {
		t.Fatalf("SearchWithRaw: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if pages, ok := raw.([]any); !ok || len(pages) != 1 {
		t.Errorf("raw = %#v, want one decoded page", raw)
	}
}

func TestSearch_Live(t *testing.T) {
	if os.Getenv("MST_ITEST") != "1" {
		t.Skip("set MST_ITEST=1 to run live Jina test")
	}
	if os.Getenv("JINA_API_KEY") == "" {
		t.Skip("JINA_API_KEY not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	results, err := New().Search(ctx, "golang concurrency", engine.Options{Limit: 3})
	if err != nil {
		t.Fatalf("live Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("live Search returned no results")
	}
}
