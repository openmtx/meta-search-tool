package pubmed

import (
	"context"
	"encoding/json"
	"fmt"
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

// minimalEsearchResponse is a valid esearch XML response with 2 PMIDs.
const minimalEsearchResponse = `<?xml version="1.0" encoding="UTF-8"?>
<eSearchResult>
  <Count>2</Count>
  <RetMax>2</RetMax>
  <IdList>
    <Id>32400405</Id>
    <Id>17255268</Id>
  </IdList>
</eSearchResult>`

// minimalESummaryResponse is a valid esummary JSON response (retmode=json) with
// 2 articles. Per-PMID objects are keyed by UID under "result".
const minimalESummaryResponse = `{
  "result": {
    "uids": ["32400405", "17255268"],
    "32400405": {"uid":"32400405","title":"Attention Is All You Need","source":"Adv Neural Inf Process Syst","pubdate":"2017 Jul 10"},
    "17255268": {"uid":"17255268","title":"Deep Residual Learning for Image Recognition","source":"Proc IEEE Conf Comput Vis Pattern Recognit","pubdate":"2016 Jun"}
  }
}`

// emptyESearchResponse has no IDs.
const emptyESearchResponse = `<?xml version="1.0" encoding="UTF-8"?>
<eSearchResult>
  <Count>0</Count>
  <RetMax>0</RetMax>
  <IdList></IdList>
</eSearchResult>`

// malformedXML is not valid XML (exercises the esearch decode error path).
const malformedXML = `not xml at all <><`

// malformedJSON is not valid JSON (exercises the esummary decode error path).
const malformedJSON = `not json at all <><`

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
	docs := []esummaryDoc{
		{Uid: "32400405", Title: "Attention Is All You Need", Source: "Adv Neural Inf Process Syst", PubDate: "2017 Jul 10"},
		{Uid: "17255268", Title: "Deep Residual Learning for Image Recognition", Source: "Proc IEEE Conf Comput Vis Pattern Recognit", PubDate: "2016 Jun"},
	}
	results, err := parseResults(docs)
	if err != nil {
		t.Fatalf("parseResults error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	want := []engine.Result{
		{Title: "Attention Is All You Need", URL: "https://pubmed.ncbi.nlm.nih.gov/32400405/", Snippet: "Adv Neural Inf Process Syst. 2017 Jul 10"},
		{Title: "Deep Residual Learning for Image Recognition", URL: "https://pubmed.ncbi.nlm.nih.gov/17255268/", Snippet: "Proc IEEE Conf Comput Vis Pattern Recognit. 2016 Jun"},
	}
	for i, w := range want {
		if results[i].Title != w.Title {
			t.Errorf("[%d] Title = %q, want %q", i, results[i].Title, w.Title)
		}
		if results[i].URL != w.URL {
			t.Errorf("[%d] URL = %q, want %q", i, results[i].URL, w.URL)
		}
		if results[i].Snippet != w.Snippet {
			t.Errorf("[%d] Snippet = %q, want %q", i, results[i].Snippet, w.Snippet)
		}
	}
}

// TestEsummaryResponse_Docs verifies the esummary JSON schema decodes into
// ordered docs (the live retmode=json shape).
func TestEsummaryResponse_Docs(t *testing.T) {
	var resp esummaryResponse
	if err := json.Unmarshal([]byte(minimalESummaryResponse), &resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	docs, err := resp.docs()
	if err != nil {
		t.Fatalf("docs error: %v", err)
	}
	if len(docs) != 2 {
		t.Fatalf("got %d docs, want 2", len(docs))
	}
	if docs[0].Uid != "32400405" || docs[0].Title != "Attention Is All You Need" {
		t.Errorf("docs[0] = %+v", docs[0])
	}
	if docs[1].Source != "Proc IEEE Conf Comput Vis Pattern Recognit" {
		t.Errorf("docs[1].Source = %q", docs[1].Source)
	}
}

func TestParseResults_Empty(t *testing.T) {
	results, err := parseResults(nil)
	if err != nil {
		t.Fatalf("parseResults error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("got %d results, want 0", len(results))
	}
}

func TestParseResults_MissingId(t *testing.T) {
	results, err := parseResults([]esummaryDoc{{Title: "No ID Article"}})
	if err != nil {
		t.Fatalf("parseResults error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("got %d results, want 0 (missing Id)", len(results))
	}
}

func TestParseResults_MissingTitle(t *testing.T) {
	results, err := parseResults([]esummaryDoc{{Uid: "12345"}})
	if err != nil {
		t.Fatalf("parseResults error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("got %d results, want 0 (missing Title)", len(results))
	}
}

func TestParseResults_MalformedJSON(t *testing.T) {
	var resp esummaryResponse
	if err := json.Unmarshal([]byte(malformedJSON), &resp); err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

// --- httptest round-trip ----------------------------------------------------

func TestSearch_Httptest(t *testing.T) {
	orig := apiBase
	defer func() { apiBase = orig }()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		switch r.URL.Path {
		case "/entrez/eutils/esearch.fcgi":
			w.Header().Set("Content-Type", "application/xml")
			w.Write([]byte(minimalEsearchResponse))
		case "/entrez/eutils/esummary.fcgi":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(minimalESummaryResponse))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer ts.Close()

	apiBase = ts.URL // host only; buildURL appends /entrez/eutils/...

	e := New()
	results, err := e.Search(context.Background(), "transformer attention", engine.Options{Limit: 10})
	if err != nil {
		t.Fatalf("Search error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	if results[0].Title != "Attention Is All You Need" {
		t.Errorf("result[0] title = %q", results[0].Title)
	}
}

func TestSearch_ContextCancelled(t *testing.T) {
	e := New()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
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

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Serve malformed XML to esearch → decode error.
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(malformedXML))
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
		name     string
		q        string
		limit    int
		wantTerm string
		wantMax  int
	}{
		{"normal query", "transformer attention", 10, "transformer attention", 10},
		{"limit capped at 10000", "quantum computing", 50000, "quantum computing", 10000},
		{"limit floored at 1", "deep learning", 0, "deep learning", 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, err := buildSearchURL(tt.q, tt.limit)
			if err != nil {
				t.Fatalf("buildSearchURL error: %v", err)
			}
			parsed, err := url.Parse(u)
			if err != nil {
				t.Fatalf("parse URL: %v", err)
			}
			if parsed.Path != "/entrez/eutils/esearch.fcgi" {
				t.Errorf("path = %s, want /entrez/eutils/esearch.fcgi", parsed.Path)
			}
			q := parsed.Query()
			if q.Get("term") != tt.wantTerm {
				t.Errorf("term = %q, want %q", q.Get("term"), tt.wantTerm)
			}
			if q.Get("db") != "pubmed" {
				t.Errorf("db = %q, want pubmed", q.Get("db"))
			}
			var max int
			if _, err := fmt.Sscanf(q.Get("retmax"), "%d", &max); err != nil {
				t.Fatalf("invalid retmax: %v", err)
			}
			if max != tt.wantMax {
				t.Errorf("retmax = %d, want %d", max, tt.wantMax)
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
		t.Fatalf("parse URL: %v", err)
	}
	if parsed.Path != "/entrez/eutils/esummary.fcgi" {
		t.Errorf("path = %s, want /entrez/eutils/esummary.fcgi", parsed.Path)
	}
	q := parsed.Query()
	if q.Get("db") != "pubmed" {
		t.Errorf("db = %q, want pubmed", q.Get("db"))
	}
	if q.Get("id") != "32400405,17255268" {
		t.Errorf("id = %q, want 32400405,17255268", q.Get("id"))
	}
	if q.Get("retmode") != "json" {
		t.Errorf("retmode = %q, want json", q.Get("retmode"))
	}
}

// --- Retry tests (shared policy via internal/engine/retry) -------------------

func fastRetry(t *testing.T) {
	t.Helper()
	retry.SetBackoffOverride(func(int) time.Duration { return 0 })
	retry.SetJitterSource(func(time.Duration) time.Duration { return 0 })
}

func TestSearch_Retry_429SucceedsOnRetry(t *testing.T) {
	fastRetry(t)

	orig := apiBase
	defer func() { apiBase = orig }()

	esearchHits := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/entrez/eutils/esearch.fcgi":
			esearchHits++
			if esearchHits == 1 {
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			w.Header().Set("Content-Type", "application/xml")
			w.Write([]byte(minimalEsearchResponse))
		case "/entrez/eutils/esummary.fcgi":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(minimalESummaryResponse))
		}
	}))
	defer ts.Close()

	apiBase = ts.URL

	e := New()
	results, err := e.Search(context.Background(), "transformer", engine.Options{Limit: 10})
	if err != nil {
		t.Fatalf("Search error after retry: %v", err)
	}
	if esearchHits != 2 {
		t.Errorf("esearch hits = %d, want 2 (initial + retry)", esearchHits)
	}
	if len(results) != 2 {
		t.Errorf("got %d results, want 2", len(results))
	}
}

func TestSearch_Retry_Exhausted(t *testing.T) {
	fastRetry(t)

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

// --- Live integration test (§14, MST_ITEST=1) -------------------------------

// TestSearch_Live verifies the engine against the real NCBI E-utilities
// endpoints — confirming the esearch/esummary paths + the esummary JSON schema.
// Skipped unless MST_ITEST=1.
func TestSearch_Live(t *testing.T) {
	if os.Getenv("MST_ITEST") != "1" {
		t.Skip("skipping live test; set MST_ITEST=1 to hit eutils.ncbi.nlm.nih.gov")
	}
	e := New()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	results, err := e.Search(ctx, "cholesterol longevity", engine.Options{Limit: 5})
	if err != nil {
		t.Fatalf("live pubmed search failed (endpoint/schema may have drifted): %v", err)
	}
	if len(results) == 0 {
		t.Fatal("live pubmed search returned 0 results")
	}
	t.Logf("pubmed live returned %d results; first: %q <%s> — %s",
		len(results), results[0].Title, results[0].URL, results[0].Snippet)
	for _, r := range results {
		if !strings.HasPrefix(r.URL, "https://pubmed.ncbi.nlm.nih.gov/") {
			t.Errorf("unexpected result URL: %s", r.URL)
		}
		if r.Title == "" {
			t.Errorf("empty title: %+v", r)
		}
	}
}
