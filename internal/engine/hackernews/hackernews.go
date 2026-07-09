// Package hackernews implements the Hacker News Algolia Search API engine (§5 Tier B).
//
// Hacker News is a keyless structured-API engine: it returns JSON via Algolia's
// public search endpoint with well-defined fields, making parsing reliable. It
// requires no API key — StatusReady at all times (unless the site is unreachable).
//
// The HTTP retry/backoff policy (§11) is shared via internal/engine/retry.
package hackernews

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/openmtx/meta-search-tool/internal/engine"
	"github.com/openmtx/meta-search-tool/internal/engine/retry"
)

// apiBase is the HN Algolia site origin. The full API path is appended in
// buildRequestURL (base.Path = "/api/v1/search") so tests can repoint apiBase
// at a local httptest origin and the request still carries the correct path.
var apiBase = "https://hn.algolia.com"

// userAgent is the HTTP User-Agent sent to HN Algolia (§12).
const userAgent = "mst/dev"

// SetAPIBase overrides the API base URL for testing. Callers must restore the
// original value after the test (typically via defer).
func SetAPIBase(base string) { apiBase = base }

// APIBase returns the current API base URL.
func APIBase() string { return apiBase }

// hackernews is the Hacker News Search engine implementation.
type hackernews struct{}

// New returns a new Hacker News engine. There is exactly one instance —
// hackernews is stateless (no per-query state, no connection pooling beyond
// stdlib).
func New() engine.Engine { return &hackernews{} }

// Name implements engine.Engine.
func (*hackernews) Name() string { return "hackernews" }

// Descriptor implements engine.Engine.
func (*hackernews) Descriptor() engine.EngineInfo {
	return engine.EngineInfo{
		Name:          "hackernews",
		Keyed:         false,
		KeyEnv:        "",
		DefaultWeight: 0.8,
		Enabled:       false, // disabled by default (SearXNG compat; enable with config or -e)
		Category:      "news",
	}
}

// Search implements engine.Engine. It sends a GET request to the Hacker News
// Algolia Search API and parses the JSON response into []engine.Result. On any
// error it returns nil results and a non-nil error. The dispatcher wraps this
// into EngineRun with timing metadata.
//
// Retry policy (§11): delegated to internal/engine/retry — up to 2 retries on
// HTTP 429/5xx and network errors, exponential backoff (base 500ms, factor 2),
// ±20% jitter, honoring Retry-After.
func (h *hackernews) Search(ctx context.Context, q string, opts engine.Options) ([]engine.Result, error) {
	results, _, err := h.fetch(ctx, q, opts)
	return results, err
}

// SearchWithRaw implements engine.RawEngine (§10 --raw): like Search but also
// returns the decoded native JSON response for --raw.
func (h *hackernews) SearchWithRaw(ctx context.Context, q string, opts engine.Options) ([]engine.Result, any, error) {
	return h.fetch(ctx, q, opts)
}

// fetch is the shared Hacker News request+parse core for Search and
// SearchWithRaw. It returns the normalized results plus the decoded native
// response (raw) for --raw; raw is nil on any pre-parse error path.
func (h *hackernews) fetch(ctx context.Context, q string, opts engine.Options) ([]engine.Result, any, error) {
	reqURL, err := buildRequestURL(q, opts.Limit)
	if err != nil {
		return nil, nil, fmt.Errorf("hackernews: build request: %w", err)
	}

	newReq := func() (*http.Request, error) {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		if err != nil {
			return nil, err
		}
		httpReq.Header.Set("User-Agent", userAgent)
		return httpReq, nil
	}

	resp, err := retry.Do(ctx, "hackernews", newReq)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("hackernews: read body: %w", err)
	}

	var apiResp apiResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, nil, fmt.Errorf("hackernews: decode response: %w", err)
	}

	results, perr := parseResults(apiResp.Hits)
	if perr != nil {
		return nil, nil, perr
	}

	// Decode the native body generically for --raw (best-effort; nil on failure).
	var raw any
	_ = json.Unmarshal(body, &raw)
	return results, raw, nil
}

// buildRequestURL constructs the Hacker News Algolia Search API URL with parameters.
// Limit is capped at 1000 (Algolia's default page size) and floored at 1.
func buildRequestURL(q string, limit int) (string, error) {
	if limit < 1 {
		limit = 1
	}
	if limit > 1000 {
		limit = 1000
	}
	base, err := url.Parse(apiBase)
	if err != nil {
		return "", err
	}
	base.Path = "/api/v1/search"
	params := base.Query()
	params.Set("query", q)
	params.Set("tags", "story")
	params.Set("hitsPerPage", fmt.Sprintf("%d", limit))
	base.RawQuery = params.Encode()
	return base.String(), nil
}

// --- Hacker News Algolia API response types (minimal) -------------------------
//
// The HN Algolia Search API returns JSON with a well-defined schema. We only
// deserialize the fields used by mst; unknown fields are ignored by the JSON
// decoder (the default behavior).
//
// Example response:
//   {
//     "hits": [{
//       "title": "Show HN: My new Go project",
//       "url": "https://example.com",
//       "points": 42,
//       "created_at": "2024-01-15T10:30:00Z",
//       "objectID": "12345"
//     }],
//     "nbHits": 100,
//     "page": 0,
//     "nbPages": 1,
//     "hitsPerPage": 10
//   }

// apiResponse is the top-level Hacker News Algolia API response.
type apiResponse struct {
	Hits        []apiHit `json:"hits"`
	NbHits      int      `json:"nbHits"`
	Page        int      `json:"page"`
	NbPages     int      `json:"nbPages"`
	HitsPerPage int      `json:"hitsPerPage"`
}

// apiHit is one result hit in the Hacker News Algolia API response.
type apiHit struct {
	Title     string  `json:"title"`
	URL       string  `json:"url"`
	Points    float64 `json:"points"`
	CreatedAt string  `json:"created_at"`
	ObjectID  string  `json:"objectID"`
}

// parseResults maps Hacker News Algolia hits into engine.Result slices. It is
// a pure function — no I/O, no side effects — making it ideal for golden-table
// tests. The HN API returns hits with title, url, points, created_at, and
// objectID. We construct the HN story URL from the objectID when the direct
// URL is missing.
func parseResults(hits []apiHit) ([]engine.Result, error) {
	results := make([]engine.Result, 0, len(hits))
	for _, hit := range hits {
		title := stripNewlines(hit.Title)

		// Use the direct URL if present; otherwise construct from objectID.
		url := hit.URL
		if url == "" && hit.ObjectID != "" {
			url = "https://news.ycombinator.com/item?id=" + hit.ObjectID
		}

		if url == "" || title == "" {
			continue // skip entries without a URL or title
		}

		// Build snippet from available metadata.
		var parts []string
		if hit.CreatedAt != "" {
			t, err := time.Parse(time.RFC3339, hit.CreatedAt)
			if err == nil {
				parts = append(parts, t.Format("2006-01-02"))
			}
		}
		if hit.Points > 0 {
			parts = append(parts, fmt.Sprintf("%d points", int(hit.Points)))
		}
		snippet := strings.Join(parts, " · ")

		results = append(results, engine.Result{
			Title:   title,
			URL:     url,
			Snippet: snippet,
		})
	}
	return results, nil
}

// stripNewlines collapses whitespace in text for clean output.
func stripNewlines(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
