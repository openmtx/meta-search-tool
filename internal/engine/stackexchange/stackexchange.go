// Package stackexchange implements the Stack Exchange Search API engine (§5 Tier B).
//
// Stack Exchange is a keyless structured-API engine: it returns JSON with well-defined
// fields, making parsing reliable. It requires no API key — StatusReady at all
// times (unless the site is unreachable).
//
// The HTTP retry/backoff policy (§11) is shared via internal/engine/retry.
package stackexchange

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

// apiBase is the Stack Exchange site origin. The full API path is appended in
// buildRequestURL (base.Path = "/2.3/search/advanced") so tests can repoint
// apiBase at a local httptest origin and the request still carries the correct
// path.
var apiBase = "https://api.stackexchange.com"

// userAgent is the HTTP User-Agent sent to Stack Exchange (§12).
const userAgent = "mst/dev"

// SetAPIBase overrides the API base URL for testing. Callers must restore the
// original value after the test (typically via defer).
func SetAPIBase(base string) { apiBase = base }

// APIBase returns the current API base URL.
func APIBase() string { return apiBase }

// stackexchange is the Stack Exchange Search engine implementation.
type stackexchange struct{}

// New returns a new Stack Exchange engine. There is exactly one instance —
// stackexchange is stateless (no per-query state, no connection pooling beyond
// stdlib).
func New() engine.Engine { return &stackexchange{} }

// Name implements engine.Engine.
func (*stackexchange) Name() string { return "stackexchange" }

// Descriptor implements engine.Engine.
func (*stackexchange) Descriptor() engine.EngineInfo {
	return engine.EngineInfo{
		Name:          "stackexchange",
		Keyed:         false,
		KeyEnv:        "",
		DefaultWeight: 1.0,
		Enabled:       true,
		Category:      "q&a",
	}
}

// Search implements engine.Engine. It sends a GET request to the Stack Exchange
// Search API and parses the JSON response into []engine.Result. On any error
// it returns nil results and a non-nil error. The dispatcher wraps this into
// EngineRun with timing metadata.
//
// Retry policy (§11): delegated to internal/engine/retry — up to 2 retries on
// HTTP 429/5xx and network errors, exponential backoff (base 500ms, factor 2),
// ±20% jitter, honoring Retry-After.
func (s *stackexchange) Search(ctx context.Context, q string, opts engine.Options) ([]engine.Result, error) {
	results, _, err := s.fetch(ctx, q, opts)
	return results, err
}

// SearchWithRaw implements engine.RawEngine (§10 --raw): like Search but also
// returns the decoded native JSON response for --raw.
func (s *stackexchange) SearchWithRaw(ctx context.Context, q string, opts engine.Options) ([]engine.Result, any, error) {
	return s.fetch(ctx, q, opts)
}

// fetch is the shared Stack Exchange request+parse core for Search and
// SearchWithRaw. It returns the normalized results plus the decoded native
// response (raw) for --raw; raw is nil on any pre-parse error path.
func (s *stackexchange) fetch(ctx context.Context, q string, opts engine.Options) ([]engine.Result, any, error) {
	reqURL, err := buildRequestURL(q, opts.Limit)
	if err != nil {
		return nil, nil, fmt.Errorf("stackexchange: build request: %w", err)
	}

	newReq := func() (*http.Request, error) {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		if err != nil {
			return nil, err
		}
		httpReq.Header.Set("User-Agent", userAgent)
		return httpReq, nil
	}

	resp, err := retry.Do(ctx, "stackexchange", newReq)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("stackexchange: read body: %w", err)
	}

	var apiResp apiResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, nil, fmt.Errorf("stackexchange: decode response: %w", err)
	}

	if apiResp.QuotaRemaining <= 0 || (apiResp.ErrorMessage != "" && len(apiResp.Items) == 0) {
		return nil, nil, fmt.Errorf("stackexchange: %s", apiResp.ErrorMessage)
	}

	results, perr := parseResults(apiResp.Items)
	if perr != nil {
		return nil, nil, perr
	}

	// Decode the native body generically for --raw (best-effort; nil on failure).
	var raw any
	_ = json.Unmarshal(body, &raw)
	return results, raw, nil
}

// buildRequestURL constructs the Stack Exchange Search API URL with parameters.
// Limit is capped at 100 (Stack Exchange's hard limit) and floored at 1.
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
	base.Path = "/2.3/search/advanced"
	params := base.Query()
	params.Set("q", q)
	params.Set("order", "desc")
	params.Set("sort", "relevance")
	params.Set("site", "stackoverflow")
	// "withbody" is a Stack Exchange filter shorthand that includes the post
	// body (used for the snippet); the API's default filter omits body.
	params.Set("filter", "withbody")
	params.Set("pagesize", fmt.Sprintf("%d", limit))
	base.RawQuery = params.Encode()
	return base.String(), nil
}

// --- Stack Exchange API response types (minimal) ------------------------------
//
// The Stack Exchange Search API returns JSON with a well-defined schema. We only
// deserialize the fields used by mst; unknown fields are ignored by the JSON
// decoder (the default behavior).
//
// Example response:
//   {
//     "items": [{
//       "question_id": 12345,
//       "title": "How do I use goroutines?",
//       "link": "https://stackoverflow.com/questions/12345",
//       "body": "...",
//       "score": 42,
//       "is_accepted": true,
//       "creation_date": 1234567890
//     }],
//     "quota_remaining": 299,
//     "has_more": false,
//     "rate_limit": 300
//   }

// apiResponse is the top-level Stack Exchange API response.
type apiResponse struct {
	Items          []apiItem `json:"items"`
	QuotaRemaining int       `json:"quota_remaining"`
	ErrorMessage   string    `json:"error_message"`
	HasMore        bool      `json:"has_more"`
	RateLimit      int       `json:"rate_limit"`
}

// apiItem is one result item in the Stack Exchange API response.
type apiItem struct {
	QuestionID   int    `json:"question_id"`
	Title        string `json:"title"`
	Link         string `json:"link"`
	Body         string `json:"body"`
	Score        int    `json:"score"`
	IsAccepted   bool   `json:"is_accepted"`
	CreationDate int64  `json:"creation_date"`
}

// parseResults maps Stack Exchange API items into engine.Result slices. It is
// a pure function — no I/O, no side effects — making it ideal for golden-table
// tests. The Stack Exchange API returns items with a question_id, title, link,
// body (HTML), score, and creation_date. We use the link as-is (it's already
// a full URL to the Stack Overflow question).
func parseResults(items []apiItem) ([]engine.Result, error) {
	results := make([]engine.Result, 0, len(items))
	for _, item := range items {
		title := stripNewlines(item.Title)
		body := stripNewlines(item.Body)

		if item.Link == "" || title == "" {
			continue // skip entries without a link or title
		}

		results = append(results, engine.Result{
			Title:   title,
			URL:     item.Link,
			Snippet: body,
		})
	}
	return results, nil
}

// stripHTML strips HTML tags from the given string.
var htmlTagRe = regexp.MustCompile(`<[^>]*>`)

// stripNewlines collapses whitespace and strips HTML tags for clean output.
func stripNewlines(s string) string {
	// Strip HTML tags first (Stack Exchange body is HTML).
	s = htmlTagRe.ReplaceAllString(s, "")
	return strings.Join(strings.Fields(s), " ")
}
