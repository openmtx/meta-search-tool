package europepmc

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

const fixturePage1 = `{
  "nextCursorMark": "AoIIPw==",
  "resultList": {"result": [
    {"id":"12345","source":"MED","title":"Attention <i>Is</i> All You Need","abstractText":"We propose a new &amp; simple network.","journalTitle":"NeurIPS","pubYear":"2017"},
    {"id":"PPR1","source":"PPR","title":"A Preprint","abstractText":"","journalTitle":"bioRxiv","pubYear":"2024"}
  ]}
}`

const fixturePage2 = `{
  "nextCursorMark": "",
  "resultList": {"result": [
    {"id":"67890","source":"PMC","title":"Third Paper","abstractText":"Third abstract."}
  ]}
}`

const emptyFixture = `{"nextCursorMark":"","resultList":{"result":[]}}`

func TestDescriptor(t *testing.T) {
	d := New().Descriptor()
	if d.Name != "europepmc" || d.Keyed || d.KeyEnv != "" || d.DefaultWeight != 1.1 || !d.Enabled || d.Category != "academic" {
		t.Errorf("Descriptor() = %+v", d)
	}
}

func TestBuildRequestURL(t *testing.T) {
	old := apiBase
	defer func() { apiBase = old }()
	apiBase = "https://example.test/rest/search"

	got, err := buildRequestURL("cancer therapy", "*")
	if err != nil {
		t.Fatalf("buildRequestURL: %v", err)
	}
	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("parse URL: %v", err)
	}
	q := u.Query()
	if q.Get("query") != "cancer therapy" || q.Get("format") != "json" || q.Get("resultType") != "core" || q.Get("cursorMark") != "*" || q.Get("pageSize") != "25" {
		t.Errorf("query params = %v", q)
	}
}

func TestParseResults(t *testing.T) {
	var resp apiResponse
	if err := json.Unmarshal([]byte(fixturePage1), &resp); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	got := parseResults(resp.ResultList.Result)
	if len(got) != 2 {
		t.Fatalf("got %d results, want 2", len(got))
	}
	if got[0].Title != "Attention Is All You Need" {
		t.Errorf("title = %q", got[0].Title)
	}
	if got[0].Snippet != "We propose a new & simple network." {
		t.Errorf("snippet = %q", got[0].Snippet)
	}
	if got[0].URL != "https://europepmc.org/article/MED/12345" {
		t.Errorf("url = %q", got[0].URL)
	}
	if got[1].Snippet != "bioRxiv. 2024" {
		t.Errorf("fallback snippet = %q, want journal/year", got[1].Snippet)
	}
}

func TestParseResults_SkipsMissingFields(t *testing.T) {
	got := parseResults([]apiResult{
		{Source: "MED", Title: "No ID"},
		{ID: "1", Source: "MED"},
		{ID: "2", Title: "No source"},
	})
	if len(got) != 0 {
		t.Errorf("got %d results, want 0", len(got))
	}
}

func TestSearch_CursorPagination(t *testing.T) {
	old := apiBase
	defer func() { apiBase = old }()

	var calls int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if got := r.Header.Get("User-Agent"); got != engine.UserAgent() {
			t.Errorf("User-Agent = %q, want %q", got, engine.UserAgent())
		}
		switch r.URL.Query().Get("cursorMark") {
		case "*":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(fixturePage1))
		case "AoIIPw==":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(fixturePage2))
		default:
			t.Errorf("unexpected cursorMark %q", r.URL.Query().Get("cursorMark"))
		}
		if n > 2 {
			t.Errorf("too many requests: %d", n)
		}
	}))
	defer ts.Close()
	apiBase = ts.URL

	results, err := New().Search(context.Background(), "cancer", engine.Options{Limit: 3})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("got %d results, want 3", len(results))
	}
	if calls != 2 {
		t.Errorf("request count = %d, want 2", calls)
	}
}

func TestSearch_Empty(t *testing.T) {
	old := apiBase
	defer func() { apiBase = old }()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(emptyFixture))
	}))
	defer ts.Close()
	apiBase = ts.URL

	results, err := New().Search(context.Background(), "nothing", engine.Options{Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("got %d results, want 0", len(results))
	}
}

func TestSearch_HTTPError(t *testing.T) {
	old := apiBase
	defer func() { apiBase = old }()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer ts.Close()
	apiBase = ts.URL

	results, err := New().Search(context.Background(), "q", engine.Options{Limit: 10})
	if err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("error = %v, want HTTP 502", err)
	}
	if results != nil {
		t.Errorf("expected nil results, got %d", len(results))
	}
}

func TestSearch_MalformedResponse(t *testing.T) {
	old := apiBase
	defer func() { apiBase = old }()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	}))
	defer ts.Close()
	apiBase = ts.URL

	if _, err := New().Search(context.Background(), "q", engine.Options{Limit: 10}); err == nil {
		t.Fatal("expected decode error")
	}
}

func TestSearch_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := New().Search(ctx, "q", engine.Options{Limit: 10}); err == nil {
		t.Fatal("expected cancellation error")
	}
}

func TestSearch_Retry429ThenSuccess(t *testing.T) {
	old := apiBase
	defer func() { apiBase = old }()
	fastRetry(t)

	var calls int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Write([]byte(fixturePage2))
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
	old := apiBase
	defer func() { apiBase = old }()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(fixturePage2))
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
	pages, ok := raw.([]any)
	if !ok || len(pages) != 1 {
		t.Errorf("raw = %#v, want one decoded page", raw)
	}
}

func TestSearch_Live(t *testing.T) {
	if os.Getenv("MST_ITEST") != "1" {
		t.Skip("set MST_ITEST=1 to run live Europe PMC test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	results, err := New().Search(ctx, "cancer immunotherapy", engine.Options{Limit: 3})
	if err != nil {
		t.Fatalf("live Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("live Search returned no results")
	}
	for i, r := range results {
		if r.Title == "" || !strings.HasPrefix(r.URL, articleBase) {
			t.Errorf("result[%d] = %+v", i, r)
		}
	}
}
