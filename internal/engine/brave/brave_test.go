package brave

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/openmtx/meta-search-tool/internal/engine"
)

// --- Golden-table: parseResults -------------------------------------------

func TestParseResults(t *testing.T) {
	resp := apiResponse{
		Web: webResponse{
			Results: []apiResult{
				{Title: "Go Programming", URL: "https://golang.org/doc", Snippet: "The Go Programming Language documentation."},
				{Title: "Go by Example", URL: "https://gobyexample.com/", Snippet: "Hands-on guide to Go."},
			},
		},
	}
	got := parseResults(resp)
	if len(got) != 2 {
		t.Fatalf("got %d results, want 2", len(got))
	}
	if got[0].Title != "Go Programming" || got[0].URL != "https://golang.org/doc" {
		t.Errorf("result[0] = %+v, want title=Go Programming url=https://golang.org/doc", got[0])
	}
	if got[1].Title != "Go by Example" || got[1].URL != "https://gobyexample.com/" {
		t.Errorf("result[1] = %+v, want title=Go by Example url=https://gobyexample.com/", got[1])
	}
}

func TestParseResults_Empty(t *testing.T) {
	if got := parseResults(apiResponse{}); len(got) != 0 {
		t.Errorf("empty response => %d results, want 0", len(got))
	}
}

func TestParseResults_SkipEmptyURL(t *testing.T) {
	resp := apiResponse{
		Web: webResponse{
			Results: []apiResult{
				{Title: "Has URL", URL: "https://example.com/1"},
				{Title: "No URL", URL: ""},
				{Title: "Another", URL: "https://example.com/2"},
			},
		},
	}
	got := parseResults(resp)
	if len(got) != 2 {
		t.Fatalf("got %d results, want 2 (empty URL skipped)", len(got))
	}
}

// --- httptest: full Search round-trip --------------------------------------

func TestSearch_Httptest(t *testing.T) {
	// Fixture: a Brave-like JSON response.
	fixture := apiResponse{
		Web: webResponse{
			Results: []apiResult{
				{Title: "A", URL: "https://example.com/a", Snippet: "snippet A"},
				{Title: "B", URL: "https://example.com/b", Snippet: "snippet B"},
			},
		},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request headers.
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Errorf("Accept = %q, want application/json", got)
		}
		if got := r.Header.Get("User-Agent"); got != userAgent {
			t.Errorf("User-Agent = %q, want %q", got, userAgent)
		}
		if got := r.Header.Get("X-Subscription-Token"); got == "" {
			t.Error("X-Subscription-Token header missing")
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(fixture)
	}))
	defer ts.Close()

	// Override apiBase to point at the test server.
	oldBase := apiBase
	apiBase = ts.URL + "/" // httptest URLs have a trailing slash
	defer func() { apiBase = oldBase }()

	// Set a fake API key.
	t.Setenv("BRAVE_API_KEY", "test-key")

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
	t.Setenv("BRAVE_API_KEY", "") // ensure unset
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
		// Block until context is done.
		<-r.Context().Done()
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	oldBase := apiBase
	apiBase = ts.URL + "/"
	defer func() { apiBase = oldBase }()

	t.Setenv("BRAVE_API_KEY", "test-key")

	ctx, cancel := context.WithCancel(context.Background())
	e := New()
	done := make(chan struct{})
	go func() {
		e.Search(ctx, "q", engine.Options{Limit: 10})
		close(done)
	}()

	cancel() // fire the cancel
	select {
	case <-done:
		// OK — Search returned promptly after cancel.
	case <-time.After(2 * time.Second):
		t.Fatal("Search did not return after context cancel")
	}
}

func TestSearch_HTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"errors":[{"message":"rate limit"}]}`))
	}))
	defer ts.Close()

	oldBase := apiBase
	apiBase = ts.URL + "/"
	defer func() { apiBase = oldBase }()

	t.Setenv("BRAVE_API_KEY", "test-key")

	e := New()
	results, err := e.Search(context.Background(), "q", engine.Options{Limit: 10})
	if err == nil {
		t.Fatal("expected error on HTTP 429")
	}
	if results != nil {
		t.Errorf("expected nil results on error, got %d", len(results))
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

	t.Setenv("BRAVE_API_KEY", "test-key")

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
	if info.Name != "brave" {
		t.Errorf("Name = %q, want brave", info.Name)
	}
	if !info.Keyed {
		t.Error("Keyed should be true")
	}
	if info.KeyEnv != "BRAVE_API_KEY" {
		t.Errorf("KeyEnv = %q, want BRAVE_API_KEY", info.KeyEnv)
	}
	if info.DefaultWeight != 2.0 {
		t.Errorf("DefaultWeight = %f, want 2.0", info.DefaultWeight)
	}
	if !info.Enabled {
		t.Error("Enabled should be true")
	}
}

// --- buildRequestURL --------------------------------------------------------

func TestBuildRequestURL(t *testing.T) {
	tests := []struct {
		q       string
		limit   int
		wantCnt string // expected count param value
	}{
		{"go", 10, "10"},
		{"go", 50, "20"}, // capped at 20
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
