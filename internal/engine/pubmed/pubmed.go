// Package pubmed implements the PubMed E-utilities API engine (§5 Tier B).
//
// PubMed is a keyless structured-API engine: it uses the NCBI E-utilities
// esearch + esummary endpoints which return well-defined XML, making parsing
// reliable. It requires no API key — StatusReady at all times (unless the
// site is unreachable).
//
// The HTTP retry/backoff policy (§11) is shared via internal/engine/retry.
package pubmed

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/openmtx/meta-search-tool/internal/engine"
	"github.com/openmtx/meta-search-tool/internal/engine/retry"
)

// apiBase is the PubMed E-utilities base URL. Tests override it via SetAPIBase.
var apiBase = "https://eutils.ncbi.nlm.nih.gov/entrez/eutils"

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
	}
}

// Search implements engine.Engine. It sends a GET request to the PubMed
// E-utilities API (esearch + esummary) and parses the XML response into
// []engine.Result. On any error it returns nil results and a non-nil error.
// The dispatcher wraps this into EngineRun with timing metadata.
//
// Retry policy (§11): delegated to internal/engine/retry — up to 2 retries on
// HTTP 429/5xx and network errors, exponential backoff (base 500ms, factor 2),
// ±20% jitter, honoring Retry-After.
func (p *pubmed) Search(ctx context.Context, q string, opts engine.Options) ([]engine.Result, error) {
	results, _, err := p.fetch(ctx, q, opts)
	return results, err
}

// SearchWithRaw implements engine.RawEngine (§10 --raw): like Search but also
// returns the decoded native XML responses for --raw.
func (p *pubmed) SearchWithRaw(ctx context.Context, q string, opts engine.Options) ([]engine.Result, any, error) {
	return p.fetch(ctx, q, opts)
}

// fetch is the shared PubMed request+parse core for Search and SearchWithRaw. It
// returns the normalized results plus the decoded native response (raw) for
// --raw; raw is nil on any pre-parse error path.
func (p *pubmed) fetch(ctx context.Context, q string, opts engine.Options) ([]engine.Result, any, error) {
	// Step 1: esearch — find matching PMIDs.
	pmidList, err := p.esearch(ctx, q, opts.Limit)
	if err != nil {
		return nil, nil, err
	}

	// Step 2: esummary — fetch details for the PMIDs.
	results, raw, err := p.esummary(ctx, pmidList)
	if err != nil {
		return nil, nil, err
	}

	return results, raw, nil
}

// esearch calls the PubMed esearch endpoint to find PMIDs matching the query.
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

	if searchResp.Error != "" {
		return nil, fmt.Errorf("pubmed: esearch error: %s", searchResp.Error)
	}

	return searchResp.IdList, nil
}

// esummary calls the PubMed esummary endpoint to fetch article details for a
// list of PMIDs.
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

	var summaryResp eSummaryResult
	if err := xml.Unmarshal(body, &summaryResp); err != nil {
		return nil, nil, fmt.Errorf("pubmed: decode esummary response: %w", err)
	}

	if summaryResp.Error != "" {
		return nil, nil, fmt.Errorf("pubmed: esummary error: %s", summaryResp.Error)
	}

	results, perr := parseResults(summaryResp)
	if perr != nil {
		return nil, nil, perr
	}

	// Decode the native body generically for --raw (best-effort; nil on failure).
	var raw any
	_ = xml.Unmarshal(body, &raw)
	return results, raw, nil
}

// buildSearchURL constructs the PubMed esearch URL with parameters.
// Limit is capped at 10000 (PubMed's practical limit) and floored at 1.
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
	base.Path = "/esearch.fcgi"
	params := base.Query()
	params.Set("db", "pubmed")
	params.Set("term", q)
	params.Set("retmax", fmt.Sprintf("%d", limit))
	params.Set("retmode", "json")
	base.RawQuery = params.Encode()
	return base.String(), nil
}

// buildSummaryURL constructs the PubMed esummary URL for a list of PMIDs.
func buildSummaryURL(pmids []string) (string, error) {
	base, err := url.Parse(apiBase)
	if err != nil {
		return "", err
	}
	base.Path = "/esummary.fcgi"
	params := base.Query()
	params.Set("db", "pubmed")
	params.Set("id", strings.Join(pmids, ","))
	params.Set("retmode", "json")
	base.RawQuery = params.Encode()
	return base.String(), nil
}

// --- PubMed E-utilities API response types (minimal) -------------------------
//
// The esearch endpoint returns XML with a <IdList> of PMID strings.
// The esummary endpoint returns XML with <DocSum> entries containing article
// metadata: Id (PMID), Title, Source, PubDate, Authors, etc.
//
// Example esearch response:
//   <?xml version="1.0" encoding="UTF-8"?>
//   <eSearchResult>
//     <Count>2</Count>
//     <RetMax>2</RetMax>
//     <IdList>
//       <Id>32400405</Id>
//       <Id>17255268</Id>
//     </IdList>
//   </eSearchResult>
//
// Example esummary response:
//   <?xml version="1.0" encoding="UTF-8"?>
//   <ArticleSet>
//     <Article>
//       <Id>32400405</Id>
//       <Title>Attention Is All You Need</Title>
//       <Source>Adv Neural Inf Process Syst</Source>
//       <PubDate>2017</PubDate>
//     </Article>
//   </ArticleSet>

// eSearchResult is the top-level response from esearch.fcgi.
type eSearchResult struct {
	XMLName xml.Name `xml:"eSearchResult"`
	IdList  []string `xml:"IdList>Id"`
	Error   string   `xml:"Error"`
}

// eSummaryResult is the top-level response from esummary.fcgi.
type eSummaryResult struct {
	XMLName  xml.Name  `xml:"ArticleSet"`
	Articles []article `xml:"Article"`
	Error    string    `xml:"Error"`
}

// article is one result entry in the PubMed esummary response.
// Note: PubMed's esummary uses lowercase element names (e.g., "title" not "Title").
type article struct {
	Id         string `xml:"Id"`
	Title      string `xml:"title"`
	Source     string `xml:"Source"`
	PubDate    string `xml:"PubDate"`
	Authors    []author
	LastAuthor string `xml:"FullJournalName"`
}

// author is an author entry in a PubMed article.
type author struct {
	Name string `xml:"Name"`
}

// parseResults maps PubMed esummary articles into engine.Result slices. It is
// a pure function — no I/O, no side effects — making it ideal for golden-table
// tests.
func parseResults(resp eSummaryResult) ([]engine.Result, error) {
	results := make([]engine.Result, 0, len(resp.Articles))
	for _, art := range resp.Articles {
		if art.Id == "" || art.Title == "" {
			continue // skip entries without an ID or title
		}

		title := stripNewlines(art.Title)

		// Construct a PubMed URL from the PMID.
		pubmedURL := "https://pubmed.ncbi.nlm.nih.gov/" + art.Id + "/"

		// Build snippet from available metadata.
		var parts []string
		if art.Source != "" {
			parts = append(parts, art.Source)
		}
		if art.PubDate != "" {
			parts = append(parts, art.PubDate)
		}
		snippet := strings.Join(parts, ". ")

		results = append(results, engine.Result{
			Title:   title,
			URL:     pubmedURL,
			Snippet: snippet,
		})
	}
	return results, nil
}

// stripNewlines collapses whitespace in text for clean output.
func stripNewlines(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
