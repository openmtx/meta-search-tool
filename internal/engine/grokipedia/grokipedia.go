// Package grokipedia implements the Grokipedia full-text search API engine (§5 Tier B).
//
// Grokipedia is a keyless structured-API engine: it returns JSON via its public
// search endpoint with well-defined fields, making parsing reliable. It requires
// no API key — StatusReady at all times (unless the site is unreachable).
//
// The HTTP retry/backoff policy (§11) is shared via internal/engine/retry.
package grokipedia

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/openmtx/meta-search-tool/internal/engine"
	"github.com/openmtx/meta-search-tool/internal/engine/retry"
)

// apiBase is the Grokipedia site origin. The full API path is appended in
// buildRequestURL (base.Path = "/api/full-text-search") so tests can repoint
// apiBase at a local httptest origin (host only) and the request still carries
// the correct path.
var apiBase = "https://grokipedia.com"

// userAgent is the HTTP User-Agent sent to Grokipedia (§12).
const userAgent = "mst/dev"

// SetAPIBase overrides the API base URL for testing. Callers must restore the
// original value after the test (typically via defer).
func SetAPIBase(base string) { apiBase = base }

// APIBase returns the current API base URL.
func APIBase() string { return apiBase }

// grokipedia is the Grokipedia Search engine implementation.
type grokipedia struct{}

// New returns a new Grokipedia engine. There is exactly one instance —
// grokipedia is stateless (no per-query state, no connection pooling beyond
// stdlib).
func New() engine.Engine { return &grokipedia{} }

// Name implements engine.Engine.
func (*grokipedia) Name() string { return "grokipedia" }

// Descriptor implements engine.Engine.
func (*grokipedia) Descriptor() engine.EngineInfo {
	return engine.EngineInfo{
		Name:          "grokipedia",
		Keyed:         false,
		KeyEnv:        "",
		DefaultWeight: 1.0,   // Tier B knowledge source
		Enabled:       false, // SearXNG marks it experimental (inactive)
		Category:      "reference",
	}
}

// Search implements engine.Engine. It sends a GET request to the Grokipedia
// full-text search API and parses the JSON response into []engine.Result. On any
// error it returns nil results and a non-nil error. The dispatcher wraps this
// into EngineRun with timing metadata.
//
// Retry policy (§11): delegated to internal/engine/retry — up to 2 retries on
// HTTP 429/5xx and network errors, exponential backoff (base 500ms, factor 2),
// ±20% jitter, honoring Retry-After.
func (g *grokipedia) Search(ctx context.Context, q string, opts engine.Options) ([]engine.Result, error) {
	results, _, err := g.fetch(ctx, q, opts)
	return results, err
}

// SearchWithRaw implements engine.RawEngine (§10 --raw): like Search but also
// returns the decoded native JSON response for --raw.
func (g *grokipedia) SearchWithRaw(ctx context.Context, q string, opts engine.Options) ([]engine.Result, any, error) {
	return g.fetch(ctx, q, opts)
}

// fetch is the shared Grokipedia request+parse core for Search and
// SearchWithRaw. It returns the normalized results plus the decoded native
// response (raw) for --raw; raw is nil on any pre-parse error path.
func (g *grokipedia) fetch(ctx context.Context, q string, opts engine.Options) ([]engine.Result, any, error) {
	reqURL, err := buildRequestURL(q, opts.Limit)
	if err != nil {
		return nil, nil, fmt.Errorf("grokipedia: build request: %w", err)
	}

	newReq := func() (*http.Request, error) {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		if err != nil {
			return nil, err
		}
		httpReq.Header.Set("User-Agent", userAgent)
		return httpReq, nil
	}

	resp, err := retry.Do(ctx, "grokipedia", newReq)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("grokipedia: read body: %w", err)
	}

	var apiResp apiResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, nil, fmt.Errorf("grokipedia: decode response: %w", err)
	}

	results, perr := parseResults(apiResp.Results)
	if perr != nil {
		return nil, nil, perr
	}

	// Decode the native body generically for --raw (best-effort; nil on failure).
	var raw any
	_ = json.Unmarshal(body, &raw)
	return results, raw, nil
}

// buildRequestURL constructs the Grokipedia full-text search API URL with parameters.
// Limit is capped at 100 and floored at 1.
func buildRequestURL(q string, limit int) (string, error) {
	if limit < 1 {
		limit = 1
	}
	if limit > 100 {
		limit = 100
	}
	base, err := url.Parse(apiBase)
	if err != nil {
		return "", err
	}
	base.Path = "/api/full-text-search"
	params := base.Query()
	params.Set("query", q)
	params.Set("limit", fmt.Sprintf("%d", limit))
	base.RawQuery = params.Encode()
	return base.String(), nil
}

// --- Grokipedia API response types (minimal) ---------------------------------
//
// The Grokipedia full-text search API returns JSON with the following schema:
//
//	{
//	  "results": [
//	    {
//	      "slug": "go-programming-language",
//	      "title": "Go Programming Language",
//	      "snippet": "Go is an open source programming language..."
//	    }
//	  ]
//	}

// apiResponse is the top-level Grokipedia API response.
type apiResponse struct {
	Results []apiResult `json:"results"`
}

// apiResult is one result from the Grokipedia API response.
type apiResult struct {
	Slug    string `json:"slug"`
	Title   string `json:"title"`
	Snippet string `json:"snippet"`
}

// htmlTagRe matches HTML tags for stripping from snippets. The Grokipedia API
// returns HTML in snippet fields (SearXNG applies html_to_text); tags are
// stripped to plain text for the normalized Result (spec §5 "html→text on
// snippet") — the same approach the stackexchange engine uses.
var htmlTagRe = regexp.MustCompile(`<[^>]*>`)

// stripHTML removes HTML tags from s (a simple regex strip, sufficient for
// snippet fields).
func stripHTML(s string) string {
	return htmlTagRe.ReplaceAllString(s, "")
}

// parseResults maps Grokipedia API results into engine.Result slices. It is
// a pure function — no I/O, no side effects — making it ideal for golden-table
// tests. The Grokipedia API returns results with slug, title, and snippet.
// URL is constructed from the slug as https://grokipedia.com/page/<slug>; the
// snippet has HTML tags stripped to plain text (spec §5).
func parseResults(results []apiResult) ([]engine.Result, error) {
	out := make([]engine.Result, 0, len(results))
	for _, r := range results {
		title := strings.TrimSpace(r.Title)
		if title == "" {
			continue // skip entries without a title
		}

		slug := strings.TrimSpace(r.Slug)
		if slug == "" {
			continue // skip entries without a slug (cannot construct URL)
		}

		// Construct URL from slug per spec: https://grokipedia.com/page/<slug>
		resultURL := "https://grokipedia.com/page/" + url.PathEscape(slug)

		snippet := strings.TrimSpace(stripHTML(r.Snippet))

		out = append(out, engine.Result{
			Title:   title,
			URL:     resultURL,
			Snippet: snippet,
		})
	}
	return out, nil
}
