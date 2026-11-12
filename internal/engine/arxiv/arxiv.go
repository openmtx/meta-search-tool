// Package arxiv implements the arXiv API engine (§5 Tier B).
//
// arXiv is a keyless structured-API engine: it returns Atom XML with well-defined
// fields, making parsing reliable. It requires no API key — StatusReady at all
// times (unless the site is unreachable).
//
// The HTTP retry/backoff policy (§11) is shared via internal/engine/retry.
package arxiv

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

// apiBase is the arXiv API endpoint. Tests override it via SetAPIBase.
var apiBase = "https://export.arxiv.org/api/query"

// SetAPIBase overrides the API base URL for testing. Callers must restore the
// original value after the test (typically via defer).
func SetAPIBase(base string) { apiBase = base }

// APIBase returns the current API base URL.
func APIBase() string { return apiBase }

// arxiv is the arXiv Search engine implementation.
type arxiv struct{}

// New returns a new arXiv engine. There is exactly one instance — arxiv is
// stateless (no per-query state, no connection pooling beyond stdlib).
func New() engine.Engine { return &arxiv{} }

// Name implements engine.Engine.
func (*arxiv) Name() string { return "arxiv" }

// Descriptor implements engine.Engine.
func (*arxiv) Descriptor() engine.EngineInfo {
	return engine.EngineInfo{
		Name:          "arxiv",
		Keyed:         false,
		KeyEnv:        "",
		DefaultWeight: 1.2,
		Enabled:       true,
		Category:      "academic",
	}
}

// Search implements engine.Engine. It sends a GET request to the arXiv API
// and parses the Atom XML response into []engine.Result. On any error (HTTP
// status >= 400, parse failure, context cancellation) it returns nil results
// and a non-nil error. The dispatcher wraps this into EngineRun with timing
// metadata.
//
// Retry policy (§11): delegated to internal/engine/retry — up to 2 retries on
// HTTP 429/5xx and network errors, exponential backoff (base 500ms, factor 2),
// ±20% jitter, honoring Retry-After.
func (a *arxiv) Search(ctx context.Context, q string, opts engine.Options) ([]engine.Result, error) {
	results, _, err := a.fetch(ctx, q, opts)
	return results, err
}

// SearchWithRaw implements engine.RawEngine (§10 --raw): like Search but also
// returns the decoded native Atom XML response (all fields, not just those mst
// maps) so --raw shows exactly what arXiv returned before normalization.
func (a *arxiv) SearchWithRaw(ctx context.Context, q string, opts engine.Options) ([]engine.Result, any, error) {
	return a.fetch(ctx, q, opts)
}

// fetch is the shared arXiv request+parse core for Search and SearchWithRaw. It
// returns the normalized results plus the decoded native response (raw) for
// --raw; raw is nil on any pre-parse error path.
func (a *arxiv) fetch(ctx context.Context, q string, opts engine.Options) ([]engine.Result, any, error) {
	reqURL, err := buildRequestURL(q, opts.Limit)
	if err != nil {
		return nil, nil, fmt.Errorf("arxiv: build request: %w", err)
	}

	// newReq builds a fresh request per retry attempt (headers are constant).
	newReq := func() (*http.Request, error) {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		if err != nil {
			return nil, err
		}
		httpReq.Header.Set("User-Agent", engine.UserAgent())
		return httpReq, nil
	}

	resp, err := retry.Do(ctx, "arxiv", newReq)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("arxiv: read body: %w", err)
	}

	var apiResp atomFeed
	if err := xml.Unmarshal(body, &apiResp); err != nil {
		return nil, nil, fmt.Errorf("arxiv: decode response: %w", err)
	}
	results, perr := parseResults(apiResp)
	if perr != nil {
		return nil, nil, perr
	}

	// Decode the native body generically for --raw (best-effort; nil on failure).
	var raw any
	_ = xml.Unmarshal(body, &raw)
	return results, raw, nil
}

// buildRequestURL constructs the arXiv API query URL with parameters.
// Limit is capped at 3000 (arXiv's hard limit) and floored at 1.
// Query construction matches SearXNG's arxiv.py: `all:<raw query>` — the
// standard arXiv API usage. Adjacent fielded terms are OR'd by the API
// (all:a b → all:a OR all:b), which is fine for academic queries; for general
// web queries arxiv is scoped by category selection (Sprint B.3.1, --engine
// academic), not by altering the query.
func buildRequestURL(q string, limit int) (string, error) {
	if limit < 1 {
		limit = 1
	}
	if limit > 3000 {
		limit = 3000
	}
	base, err := url.Parse(apiBase)
	if err != nil {
		return "", err
	}
	params := base.Query()
	params.Set("search_query", "all:"+q)
	params.Set("start", "0")
	params.Set("max_results", fmt.Sprintf("%d", limit))
	base.RawQuery = params.Encode()
	return base.String(), nil
}

// --- arXiv Atom API response types (minimal) -------------------------------
//
// The arXiv API returns an Atom XML feed. We only deserialize the fields used
// by mst; unknown elements are ignored by the XML decoder (the default behavior).
//
// Example response:
//   <?xml version='1.0' encoding='UTF-8'?>
//   <feed xmlns="http://www.w3.org/2005/Atom" ...>
//     <entry>
//       <id>http://arxiv.org/abs/1706.03762</id>
//       <title>Attention Is All You Need</title>
//       <summary>We introduce a new simple network architecture...</summary>
//       <link href="https://arxiv.org/abs/1706.03762" rel="alternate"/>
//       ...
//     </entry>
//   </feed>

const atomNS = "http://www.w3.org/2005/Atom"

// atomFeed is the top-level Atom feed from the arXiv API.
type atomFeed struct {
	XMLName xml.Name    `xml:"http://www.w3.org/2005/Atom feed"`
	Entries []atomEntry `xml:"http://www.w3.org/2005/Atom entry"`
}

// atomEntry is one result entry in the arXiv Atom feed.
type atomEntry struct {
	ID      string     `xml:"id"`
	Title   string     `xml:"title"`
	Summary string     `xml:"summary"`
	Links   []atomLink `xml:"link"`
}

// atomLink is a link element within an arXiv entry.
type atomLink struct {
	HRef string `xml:"href,attr"`
	Rel  string `xml:"rel,attr"`
}

// parseResults maps arXiv Atom feed entries into engine.Result slices. It is a
// pure function — no I/O, no side effects — making it ideal for golden-table
// tests. The arXiv API returns entries with an <id> (arxiv identifier),
// <title>, <summary>, and <link rel="alternate"> (the abs URL). We use the
// https://arxiv.org/abs/<id> URL as the canonical result URL.
func parseResults(feed atomFeed) ([]engine.Result, error) {
	results := make([]engine.Result, 0, len(feed.Entries))
	for _, entry := range feed.Entries {
		title := stripNewlines(entry.Title)
		summary := stripNewlines(entry.Summary)

		// Find the alternate link (the abs URL).
		var absURL string
		for _, link := range entry.Links {
			if link.Rel == "alternate" && link.HRef != "" {
				absURL = link.HRef
				break
			}
		}
		if absURL == "" {
			continue // skip entries without an alternate link
		}

		results = append(results, engine.Result{
			Title:   title,
			URL:     absURL,
			Snippet: summary,
		})
	}
	return results, nil
}

// stripNewlines collapses whitespace in title/summary text for clean output.
func stripNewlines(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
