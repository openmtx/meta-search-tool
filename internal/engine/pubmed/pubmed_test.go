package pubmed

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/openmtx/meta-search-tool/internal/engine"
	"github.com/openmtx/meta-search-tool/internal/engine/retry"
)

// minimalEsearchResponse is a valid esearch response with 2 PMIDs.
const minimalEsearchResponse = `<?xml version="1.0" encoding="UTF-8"?>
<eSearchResult>
  <Count>2</Count>
  <RetMax>2</RetMax>
  <IdList>
    <Id>32400405</Id>
    <Id>17255268</Id>
  </IdList>
</eSearchResult>`

// minimalESummaryResponse is a valid esummary response with 2 articles.
const minimalESummaryResponse = `<?xml version="1.0" encoding="UTF-8"?>
<ArticleSet>
  <Article>
    <Id>32400405</Id>
    <title>Attention Is All You Need</title>
    <Source>Adv Neural Inf Process Syst</Source>
    <PubDate>2017 Jul 10</PubDate>
  </Article>
  <Article>
    <Id>17255268</Id>
    <title>Deep Residual Learning for Image Recognition</title>
    <Source>Proc IEEE Conf Comput Vis Pattern Recognit</Source>
    <PubDate>2016 Jun</PubDate>
  </Article>
</ArticleSet>`

// emptyESearchResponse has no IDs.
const emptyESearchResponse = `<?xml version="1.0" encoding="UTF-8"?>
<eSearchResult>
  <Count>0</Count>
  <RetMax>0</RetMax>
  <IdList></IdList>
</eSearchResult>`

// emptyESummaryResponse has no articles.
const emptyESummaryResponse = `<?xml version="1.0" encoding="UTF-8"?>
<ArticleSet></ArticleSet>`

// malformedXML is not valid XML.
const malformedXML = `not xml at all <><`

// fullRoundTripFixture simulates a complete esearch→esummary round trip.
type fullRoundTripFixture struct {
	esearchBody string
	summaryBody string
}

func TestDescriptor(t *testing.T) {
	e := New()
	d := e.Descriptor()
	if d.Name != "pubmed" {
		t.Errorf("Name = %q, want %q", d.Name, "pubmed")
	}
	if d.Keyed {
		t.Error("Keyed should be false")
	}
	if d.KeyEnv != "" {
		t.Errorf("KeyEnv = %q, want empty", d.KeyEnv)
	}
	if d.DefaultWeight != 1.2 {
		t.Errorf("DefaultWeight = %v, want 1.2", d.DefaultWeight)
	}
	if !d.Enabled {
		t.Error("Enabled should be true")
	}
}

func TestParseResults(t *testing.T) {
	var resp eSummaryResult
	if err := xml.Unmarshal([]byte(minimalESummaryResponse), &resp); err != nil {
		t.Fatalf("failed to parse fixture: %v", err)
	}

	results, err := parseResults(resp)
	if err != nil {
		t.Fatalf("parseResults error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}

	want := []engine.Result{
		{
			Title:   "Attention Is All You Need",
			URL:     "https://pubmed.ncbi.nlm.nih.gov/32400405/",
			Snippet: "Adv Neural Inf Process Syst. 2017 Jul 10",
		},
		{
			Title:   "Deep Residual Learning for Image Recognition",
			URL:     "https://pubmed.ncbi.nlm.nih.gov/17255268/",
			Snippet: "Proc IEEE Conf Comput Vis Pattern Recognit. 2016 Jun",
		},
	}

	for i, w := range want {
		if results[i].Title != w.Title {
			t.Errorf("[%d] Title = %q, want %q", i, results[i].Title, w.Title)
		}
		if results[i].URL != w.URL {
			t.Errorf("[%d] URL = %q, want %q", i, results[i].URL, w.Title)
		}
		if results[i].Snippet != w.Snippet {
			t.Errorf("[%d] Snippet = %q, want %q", i, results[i].Snippet, w.Title)
		}
	}
}

func TestParseResults_Empty(t *testing.T) {
	var resp eSummaryResult
	if err := xml.Unmarshal([]byte(emptyESummaryResponse), &resp); err != nil {
		t.Fatalf("failed to parse fixture: %v", err)
	}
	results, err := parseResults(resp)
	if err != nil {
		t.Fatalf("parseResults error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("got %d results, want 0", len(results))
	}
}

func TestParseResults_MissingId(t *testing.T) {
	// Entry with no Id.
	resp := eSummaryResult{
		Articles: []article{
			{Title: "No ID Article"},
		},
	}
	results, err := parseResults(resp)
	if err != nil {
		t.Fatalf("parseResults error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("got %d results, want 0 (missing Id)", len(results))
	}
}

func TestParseResults_MissingTitle(t *testing.T) {
	// Entry with no Title.
	resp := eSummaryResult{
		Articles: []article{
			{Id: "12345"},
		},
	}
	results, err := parseResults(resp)
	if err != nil {
		t.Fatalf("parseResults error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("got %d results, want 0 (missing Title)", len(results))
	}
}

func TestParseResults_MalformedXML(t *testing.T) {
	var resp eSummaryResult
	err := xml.Unmarshal([]byte(malformedXML), &resp)
	if err == nil {
		t.Fatal("expected error for malformed XML")
	}
}

// --- httptest round-trip ----------------------------------------------------

func TestSearch_Httptest(t *testing.T) {
	orig := apiBase
	defer func() { apiBase = orig }()

	count := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		switch count {
		case 1:
			// esearch call.
			if r.Method != http.MethodGet {
				t.Errorf("esearch method = %s, want GET", r.Method)
			}
			parsed, err := url.Parse(r.URL.String())
			if err != nil {
				t.Fatalf("failed to parse request URL: %v", err)
			}
			if parsed.Path != "/esearch.fcgi" {
				t.Errorf("esearch path = %s, want /esearch.fcgi", parsed.Path)
			}
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(minimalEsearchResponse))
		case 2:
			// esummary call.
			if r.Method != http.MethodGet {
				t.Errorf("esummary method = %s, want GET", r.Method)
			}
			parsed, err := url.Parse(r.URL.String())
			if err != nil {
				t.Fatalf("failed to parse request URL: %v", err)
			}
			if parsed.Path != "/esummary.fcgi" {
				t.Errorf("esummary path = %s, want /esummary.fcgi", parsed.Path)
			}
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(minimalESummaryResponse))
		default:
			t.Errorf("unexpected request #%d", count)
		}
	}))
	defer ts.Close()

	apiBase = ts.URL + "/entrez/eutils"

	e := New()
	results, err := e.Search(context.Background(), "transformer attention", engine.Options{Limit: 10})
	if err != nil {
		t.Fatalf("Search error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	if count != 2 {
		t.Errorf("got %d requests, want 2 (esearch + esummary)", count)
	}
}

func TestSearch_ContextCancelled(t *testing.T) {
	e := New()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately
	_, err := e.Search(ctx, "transformer", engine.Options{Limit: 10})
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestSearch_HTTPError(t *testing.T) {
	orig := apiBase
	defer func() { apiBase = orig }()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte("502 Bad Gateway"))
	}))
	defer ts.Close()

	apiBase = ts.URL

	e := New()
	results, err := e.Search(context.Background(), "transformer", engine.Options{Limit: 10})
	if err == nil {
		t.Fatal("expected error for HTTP 502")
	}
	if results != nil {
		t.Error("expected nil results on HTTP error")
	}
}

func TestSearch_MalformedResponse(t *testing.T) {
	orig := apiBase
	defer func() { apiBase = orig }()

	count := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusOK)
		if count == 1 {
			w.Write([]byte(malformedXML))
		} else {
			w.Write([]byte(malformedXML))
		}
	}))
	defer ts.Close()

	apiBase = ts.URL

	e := New()
	results, err := e.Search(context.Background(), "transformer", engine.Options{Limit: 10})
	if err == nil {
		t.Fatal("expected error for malformed response")
	}
	if results != nil {
		t.Error("expected nil results on parse failure")
	}
}

func TestSearch_EsearchReturnsEmpty(t *testing.T) {
	orig := apiBase
	defer func() { apiBase = orig }()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(emptyESearchResponse))
	}))
	defer ts.Close()

	apiBase = ts.URL

	e := New()
	results, err := e.Search(context.Background(), "nonexistent query xyz123", engine.Options{Limit: 10})
	if err != nil {
		t.Fatalf("Search error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("got %d results, want 0 (esearch returned no IDs)", len(results))
	}
}

// --- buildRequestURL tests --------------------------------------------------

func TestBuildSearchURL(t *testing.T) {
	tests := []struct {
		name      string
		q         string
		limit     int
		wantQuery string
		wantMax   int
	}{
		{
			name:      "normal query",
			q:         "transformer attention",
			limit:     10,
			wantQuery: "transformer attention",
			wantMax:   10,
		},
		{
			name:      "limit capped at 10000",
			q:         "quantum computing",
			limit:     50000,
			wantQuery: "quantum computing",
			wantMax:   10000,
		},
		{
			name:      "limit floored at 1",
			q:         "deep learning",
			limit:     0,
			wantQuery: "deep learning",
			wantMax:   1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, err := buildSearchURL(tt.q, tt.limit)
			if err != nil {
				t.Fatalf("buildSearchURL error: %v", err)
			}
			parsed, err := url.Parse(u)
			if err != nil {
				t.Fatalf("failed to parse result URL: %v", err)
			}
			if parsed.Path != "/esearch.fcgi" {
				t.Errorf("path = %s, want /esearch.fcgi", parsed.Path)
			}
			q := parsed.Query()
			if q.Get("term") != tt.wantQuery {
				t.Errorf("term = %q, want %q", q.Get("term"), tt.wantQuery)
			}
			if q.Get("db") != "pubmed" {
				t.Errorf("db = %q, want pubmed", q.Get("db"))
			}
			if q.Get("retmax") != "" {
				var max int
				_, err := fmt.Sscanf(q.Get("retmax"), "%d", &max)
				if err != nil {
					t.Fatalf("invalid retmax: %v", err)
				}
				if max != tt.wantMax {
					t.Errorf("retmax = %d, want %d", max, tt.wantMax)
				}
			} else {
				t.Error("retmax is empty")
			}
		})
	}
}

func TestBuildSummaryURL(t *testing.T) {
	u, err := buildSummaryURL([]string{"32400405", "17255268"})
	if err != nil {
		t.Fatalf("buildSummaryURL error: %v", err)
	}
	parsed, err := url.Parse(u)
	if err != nil {
		t.Fatalf("failed to parse result URL: %v", err)
	}
	if parsed.Path != "/esummary.fcgi" {
		t.Errorf("path = %s, want /esummary.fcgi", parsed.Path)
	}
	q := parsed.Query()
	if q.Get("db") != "pubmed" {
		t.Errorf("db = %q, want pubmed", q.Get("db"))
	}
	if q.Get("id") != "32400405,17255268" {
		t.Errorf("id = %q, want 32400405,17255268", q.Get("id"))
	}
}

// --- Retry tests (shared policy via internal/engine/retry) -------------------

func fastRetry(t *testing.T) func() {
	t.Helper()
	retry.SetBackoffOverride(func(attempt int) time.Duration {
		return 10 * time.Millisecond // fast for tests
	})
	retry.SetJitterSource(nil) // deterministic
	return func() { retry.SetBackoffOverride(nil) }
}

func TestSearch_Retry_429SucceedsOnRetry(t *testing.T) {
	defer fastRetry(t)()

	orig := apiBase
	defer func() { apiBase = orig }()

	count := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		switch count {
		case 1:
			// First esearch call → 429.
			w.WriteHeader(http.StatusTooManyRequests)
		case 2:
			// Second attempt: esearch succeeds, serve esummary response.
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(minimalEsearchResponse))
		default:
			// Subsequent calls are for esummary.
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(minimalESummaryResponse))
		}
	}))
	defer ts.Close()

	apiBase = ts.URL + "/entrez/eutils"

	e := New()
	results, err := e.Search(context.Background(), "transformer", engine.Options{Limit: 10})
	if err != nil {
		t.Fatalf("Search error after retry: %v", err)
	}
	if count < 2 {
		t.Errorf("got %d requests, want at least 2 (initial + 1 retry)", count)
	}
	if len(results) != 2 {
		t.Errorf("got %d results, want 2", len(results))
	}
}

func TestSearch_Retry_Exhausted(t *testing.T) {
	defer fastRetry(t)()

	orig := apiBase
	defer func() { apiBase = orig }()

	count := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer ts.Close()

	apiBase = ts.URL

	e := New()
	results, err := e.Search(context.Background(), "transformer", engine.Options{Limit: 10})
	if err == nil {
		t.Fatal("expected error after retry exhaustion")
	}
	if results != nil {
		t.Error("expected nil results on retry exhaustion")
	}
	if count != 3 {
		t.Errorf("got %d requests, want 3 (initial + 2 retries)", count)
	}
}
