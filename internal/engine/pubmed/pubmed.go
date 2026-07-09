// Package pubmed implements the PubMed E-utilities API engine (§5 Tier B).
//
// PubMed is a keyless structured-API engine: it uses the NCBI E-utilities
// esearch + esummary endpoints, making parsing reliable. It requires no API
// key — StatusReady at all times (unless the site is unreachable).
//
// esearch returns XML (default retmode) carrying just the matching PMID list;
// esummary is requested with retmode=json because its JSON schema
// ({result:{uids:[...],<pmid>:{title,source,pubdate,...}}}) is far cleaner than
// the <Item Name="..."> key-value XML form.
//
// The HTTP retry/backoff policy (§11) is shared via internal/engine/retry.
package pubmed

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/openmtx/meta-search-tool/internal/engine"
	"github.com/openmtx/meta-search-tool/internal/engine/retry"
)

// apiBase is the NCBI site origin. The full E-utilities paths are appended in
// buildSearchURL/buildSummaryURL (base.Path = "/entrez/eutils/...") so tests can
// repoint apiBase at a local httptest origin and the request still carries the
// correct path.
var apiBase = "https://eutils.ncbi.nlm.nih.gov"

// userAgent is the HTTP User-Agent sent to PubMed (§12).
const userAgent = "mst/dev"

// SetAPIBase overrides the API base URL for testing. Callers must restore the
// original value after the test (typically via defer).
func SetAPIBase(base string) { apiBase = base }

// APIBase returns the current API base URL.
func APIBase() string { return apiBase }

// pubmed is the PubMed Search engine implementation.
type pubmed struct{}

// New returns a new PubMed engine. There is exactly one instance — pubmed is
// stateless (no per-query state, no connection pooling beyond stdlib).
func New() engine.Engine { return &pubmed{} }

// Name implements engine.Engine.
func (*pubmed) Name() string { return "pubmed" }

// Descriptor implements engine.Engine.
func (*pubmed) Descriptor() engine.EngineInfo {
	return engine.EngineInfo{
		Name:          "pubmed",
		Keyed:         false,
		KeyEnv:        "",
		DefaultWeight: 1.2,
		Enabled:       true,
		Category:      "academic",
	}
}

// Search implements engine.Engine. It runs esearch (find PMIDs) then esummary
// (fetch details) and parses into []engine.Result. On any error it returns nil
// results and a non-nil error.
//
// Retry policy (§11): delegated to internal/engine/retry — up to 2 retries on
// HTTP 429/5xx and network errors, exponential backoff, ±20% jitter, honoring
// Retry-After.
func (p *pubmed) Search(ctx context.Context, q string, opts engine.Options) ([]engine.Result, error) {
	results, _, err := p.fetch(ctx, q, opts)
	return results, err
}

// SearchWithRaw implements engine.RawEngine (§10 --raw): like Search but also
// returns the decoded native esummary JSON for --raw.
func (p *pubmed) SearchWithRaw(ctx context.Context, q string, opts engine.Options) ([]engine.Result, any, error) {
	return p.fetch(ctx, q, opts)
}

// fetch is the shared PubMed request+parse core for Search and SearchWithRaw.
func (p *pubmed) fetch(ctx context.Context, q string, opts engine.Options) ([]engine.Result, any, error) {
	pmids, err := p.esearch(ctx, q, opts.Limit)
	if err != nil {
		return nil, nil, err
	}
	return p.esummary(ctx, pmids)
}

// esearch calls esearch.fcgi (XML) to find PMIDs matching the query.
func (p *pubmed) esearch(ctx context.Context, q string, limit int) ([]string, error) {
	reqURL, err := buildSearchURL(q, limit)
	if err != nil {
		return nil, fmt.Errorf("pubmed: build search URL: %w", err)
	}

	newReq := func() (*http.Request, error) {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		if err != nil {
			return nil, err
		}
		httpReq.Header.Set("User-Agent", userAgent)
		return httpReq, nil
	}

	resp, err := retry.Do(ctx, "pubmed", newReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("pubmed: read esearch body: %w", err)
	}

	var searchResp eSearchResult
	if err := xml.Unmarshal(body, &searchResp); err != nil {
		return nil, fmt.Errorf("pubmed: decode esearch response: %w", err)
	}
	return searchResp.IdList, nil
}

// esummary calls esummary.fcgi (retmode=json) to fetch article details for a
// list of PMIDs, returning normalized results plus the decoded native JSON.
func (p *pubmed) esummary(ctx context.Context, pmids []string) ([]engine.Result, any, error) {
	if len(pmids) == 0 {
		return nil, nil, nil
	}

	reqURL, err := buildSummaryURL(pmids)
	if err != nil {
		return nil, nil, fmt.Errorf("pubmed: build summary URL: %w", err)
	}

	newReq := func() (*http.Request, error) {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		if err != nil {
			return nil, err
		}
		httpReq.Header.Set("User-Agent", userAgent)
		return httpReq, nil
	}

	resp, err := retry.Do(ctx, "pubmed", newReq)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("pubmed: read esummary body: %w", err)
	}

	var sumResp esummaryResponse
	if err := json.Unmarshal(body, &sumResp); err != nil {
		return nil, nil, fmt.Errorf("pubmed: decode esummary response: %w", err)
	}

	docs, err := sumResp.docs()
	if err != nil {
		return nil, nil, fmt.Errorf("pubmed: parse esummary docs: %w", err)
	}
	results, perr := parseResults(docs)
	if perr != nil {
		return nil, nil, perr
	}

	// Decode the native body generically for --raw (best-effort; nil on failure).
	var raw any
	_ = json.Unmarshal(body, &raw)
	return results, raw, nil
}

// buildSearchURL constructs the esearch URL. Limit is capped at 10000 and
// floored at 1. esearch defaults to XML (no retmode set).
func buildSearchURL(q string, limit int) (string, error) {
	if limit < 1 {
		limit = 1
	}
	if limit > 10000 {
		limit = 10000
	}
	base, err := url.Parse(apiBase)
	if err != nil {
		return "", err
	}
	base.Path = "/entrez/eutils/esearch.fcgi"
	params := base.Query()
	params.Set("db", "pubmed")
	params.Set("term", q)
	params.Set("retmax", fmt.Sprintf("%d", limit))
	base.RawQuery = params.Encode()
	return base.String(), nil
}

// buildSummaryURL constructs the esummary URL for a list of PMIDs (retmode=json).
func buildSummaryURL(pmids []string) (string, error) {
	base, err := url.Parse(apiBase)
	if err != nil {
		return "", err
	}
	base.Path = "/entrez/eutils/esummary.fcgi"
	params := base.Query()
	params.Set("db", "pubmed")
	params.Set("id", strings.Join(pmids, ","))
	params.Set("retmode", "json")
	base.RawQuery = params.Encode()
	return base.String(), nil
}

// --- PubMed E-utilities response types ---------------------------------------
//
// esearch.fcgi (XML, default retmode):
//
//	<?xml version="1.0"?>
//	<eSearchResult>
//	  <IdList><Id>32400405</Id><Id>17255268</Id></IdList>
//	</eSearchResult>
//
// esummary.fcgi (retmode=json) — per-UID objects have dynamic keys:
//
//	{"result":{"uids":["32400405"],
//	           "32400405":{"uid":"32400405","title":"...","source":"...",
//	                       "pubdate":"2020 May 10", ...}}}

// eSearchResult is the top-level esearch XML response.
type eSearchResult struct {
	XMLName xml.Name `xml:"eSearchResult"`
	IdList  []string `xml:"IdList>Id"`
}

// esummaryResponse is the top-level esummary JSON response. Result maps the
// literal "uids" key to the UID list and each UID to its article object, so it
// is kept as raw JSON and decoded in order by docs().
type esummaryResponse struct {
	Result map[string]json.RawMessage `json:"result"`
}

// esummaryDoc is one article in the esummary JSON result.
type esummaryDoc struct {
	Uid     string `json:"uid"`
	Title   string `json:"title"`
	Source  string `json:"source"`
	PubDate string `json:"pubdate"`
}

// docs extracts the articles from the esummary response in UID order. A missing
// "uids" key yields no docs (e.g. an empty result set).
func (r esummaryResponse) docs() ([]esummaryDoc, error) {
	uidRaw, ok := r.Result["uids"]
	if !ok {
		return nil, nil
	}
	var uids []string
	if err := json.Unmarshal(uidRaw, &uids); err != nil {
		return nil, err
	}
	out := make([]esummaryDoc, 0, len(uids))
	for _, uid := range uids {
		docRaw, ok := r.Result[uid]
		if !ok {
			continue
		}
		var doc esummaryDoc
		if err := json.Unmarshal(docRaw, &doc); err != nil {
			return nil, err
		}
		out = append(out, doc)
	}
	return out, nil
}

// parseResults maps PubMed esummary docs into engine.Result slices. It is a
// pure function — no I/O, no side effects — ideal for golden-table tests. The
// PubMed URL is constructed from the PMID.
func parseResults(docs []esummaryDoc) ([]engine.Result, error) {
	results := make([]engine.Result, 0, len(docs))
	for _, doc := range docs {
		id := strings.TrimSpace(doc.Uid)
		title := stripNewlines(doc.Title)
		if id == "" || title == "" {
			continue // skip entries without a PMID or title
		}

		var parts []string
		if doc.Source != "" {
			parts = append(parts, doc.Source)
		}
		if doc.PubDate != "" {
			parts = append(parts, doc.PubDate)
		}

		results = append(results, engine.Result{
			Title:   title,
			URL:     "https://pubmed.ncbi.nlm.nih.gov/" + id + "/",
			Snippet: strings.Join(parts, ". "),
		})
	}
	return results, nil
}

// stripNewlines collapses whitespace in text for clean output.
func stripNewlines(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
