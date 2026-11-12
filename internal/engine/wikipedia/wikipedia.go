// Package wikipedia implements the Wikipedia OpenSearch engine (§5 Tier B).
//
// Wikipedia is a keyless structured-API engine: it returns JSON with well-defined
// fields, making parsing reliable and block-resistant. It requires no API key —
// StatusReady at all times (unless the site is unreachable).
//
// The HTTP retry/backoff policy (§11) is shared via internal/engine/retry.
package wikipedia

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/openmtx/meta-search-tool/internal/engine"
	"github.com/openmtx/meta-search-tool/internal/engine/retry"
)

// apiResponse is the Wikipedia OpenSearch response: [query, [titles], [descriptions], [urls]].
// The first element is a string (the query); elements 1-3 are arrays of strings.
type apiResponse []json.RawMessage

// apiBase is the Wikipedia OpenSearch endpoint. Tests override it via SetAPIBase.
var apiBase = "https://en.wikipedia.org/w/api.php"

// SetAPIBase overrides the API base URL for testing. Callers must restore the
// original value after the test (typically via defer).
func SetAPIBase(base string) { apiBase = base }

// APIBase returns the current API base URL.
func APIBase() string { return apiBase }

// wikipedia is the Wikipedia Search engine implementation.
type wikipedia struct{}

// New returns a new Wikipedia engine. There is exactly one instance — wikipedia
// is stateless (no per-query state, no connection pooling beyond stdlib).
func New() engine.Engine { return &wikipedia{} }

// Name implements engine.Engine.
func (*wikipedia) Name() string { return "wikipedia" }

// Descriptor implements engine.Engine.
func (*wikipedia) Descriptor() engine.EngineInfo {
	return engine.EngineInfo{
		Name:          "wikipedia",
		Keyed:         false,
		KeyEnv:        "",
		DefaultWeight: 1.5,
		Enabled:       true,
		Category:      "reference",
	}
}

// Search implements engine.Engine. It sends a GET request to the Wikipedia
// OpenSearch API and parses the response into []engine.Result. On any error
// (HTTP status >= 400, parse failure, context cancellation) it returns nil
// results and a non-nil error. The dispatcher wraps this into EngineRun with
// timing metadata.
//
// Retry policy (§11): delegated to internal/engine/retry — up to 2 retries on
// HTTP 429/5xx and network errors, exponential backoff (base 500ms, factor 2),
// ±20% jitter, honoring Retry-After.
func (w *wikipedia) Search(ctx context.Context, q string, opts engine.Options) ([]engine.Result, error) {
	results, _, err := w.fetch(ctx, q, opts)
	return results, err
}

// SearchWithRaw implements engine.RawEngine (§10 --raw): like Search but also
// returns the decoded native OpenSearch JSON ([query, [titles], [descs], [urls]])
// so --raw shows exactly what Wikipedia returned before normalization.
func (w *wikipedia) SearchWithRaw(ctx context.Context, q string, opts engine.Options) ([]engine.Result, any, error) {
	return w.fetch(ctx, q, opts)
}

// fetch is the shared Wikipedia request+parse core for Search and SearchWithRaw.
func (w *wikipedia) fetch(ctx context.Context, q string, opts engine.Options) ([]engine.Result, any, error) {
	reqURL, err := buildRequestURL(q, opts.Limit)
	if err != nil {
		return nil, nil, fmt.Errorf("wikipedia: build request: %w", err)
	}

	// newReq builds a fresh request per retry attempt (headers are constant).
	newReq := func() (*http.Request, error) {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		if err != nil {
			return nil, err
		}
		httpReq.Header.Set("Accept", "application/json")
		httpReq.Header.Set("User-Agent", engine.UserAgent())
		return httpReq, nil
	}

	resp, err := retry.Do(ctx, "wikipedia", newReq)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("wikipedia: read body: %w", err)
	}

	var apiResp apiResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, nil, fmt.Errorf("wikipedia: decode response: %w", err)
	}
	results, perr := parseResults(apiResp)
	if perr != nil {
		return nil, nil, perr
	}

	// Decode the native body generically for --raw (best-effort; nil on failure).
	var raw any
	_ = json.Unmarshal(body, &raw)
	return results, raw, nil
}

// buildRequestURL constructs the Wikipedia OpenSearch API query URL with parameters.
// Limit is capped at 20 and floored at 1 (Wikipedia's page-size limits).
func buildRequestURL(q string, limit int) (string, error) {
	if limit < 1 {
		limit = 1
	}
	if limit > 20 {
		limit = 20
	}
	base, err := url.Parse(apiBase)
	if err != nil {
		return "", err
	}
	params := base.Query()
	params.Set("action", "opensearch")
	params.Set("search", q)
	params.Set("limit", fmt.Sprintf("%d", limit))
	params.Set("format", "json")
	base.RawQuery = params.Encode()
	return base.String(), nil
}

// --- Wikipedia OpenSearch API response types (minimal) ----------------------
//
// The Wikipedia OpenSearch API returns a JSON array of four arrays:
// [query, [titles], [descriptions/snippets], [urls]].
// Example: ["golang", ["Golang"], ["Go programming language"], ["https://en.wikipedia.org/wiki/Golang"]]

// parseResults maps Wikipedia OpenSearch results into engine.Result slices. It
// is a pure function — no I/O, no side effects — making it ideal for
// golden-table tests. The Wikipedia API returns [query, [titles], [descriptions], [urls]]
// where descriptions may be empty strings (no snippet). Per-element unmarshal
// errors are surfaced, so a payload that is a valid JSON array but whose inner
// elements are not string arrays (e.g. a schema change) is reported as
// malformed rather than silently flattening to empty results. Mismatched
// lengths across the three arrays are tolerated by taking the shortest (not an
// error). The first element is the query string (ignored).
func parseResults(resp apiResponse) ([]engine.Result, error) {
	const minArrays = 4 // [query, [titles], [descriptions], [urls]]
	if len(resp) < minArrays {
		return nil, fmt.Errorf("wikipedia: malformed response: expected %d elements, got %d", minArrays, len(resp))
	}
	var titles, snippets, urls []string
	if err := json.Unmarshal(resp[1], &titles); err != nil {
		return nil, fmt.Errorf("wikipedia: malformed response: decode titles: %w", err)
	}
	if err := json.Unmarshal(resp[2], &snippets); err != nil {
		return nil, fmt.Errorf("wikipedia: malformed response: decode descriptions: %w", err)
	}
	if err := json.Unmarshal(resp[3], &urls); err != nil {
		return nil, fmt.Errorf("wikipedia: malformed response: decode urls: %w", err)
	}

	n := len(titles)
	if n > len(snippets) {
		n = len(snippets)
	}
	if n > len(urls) {
		n = len(urls)
	}

	results := make([]engine.Result, 0, n)
	for i := 0; i < n; i++ {
		title := titles[i]
		u := urls[i]
		if title == "" || u == "" {
			continue
		}
		results = append(results, engine.Result{
			Title:   title,
			URL:     u,
			Snippet: strings.TrimSpace(snippets[i]),
		})
	}
	return results, nil
}
