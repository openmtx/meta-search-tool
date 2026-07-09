// Package tavily implements the Tavily Search API engine (§5 Tier D).
//
// Tavily is a keyed JSON API engine designed for AI-oriented search. It returns
// structured results with URLs, titles, and content (snippets), plus an optional
// synthesized answer field. It requires a TAVILY_API_KEY environment variable;
// without it, the engine auto-skips (StatusMissingKey) via the registry's
// key-check in resolvePlan.
//
// The HTTP retry/backoff policy (§11) is shared via internal/engine/retry.
//
// Extra options (engine.Options):
//   - IncludeAnswer: request AI-generated answer in the response (default false)
//   - SearchDepth: "basic" (default) or "advanced"
package tavily

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/openmtx/meta-search-tool/internal/engine"
	"github.com/openmtx/meta-search-tool/internal/engine/retry"
)

// apiBase is the Tavily Search API endpoint. Tests override it via SetAPIBase.
var apiBase = "https://api.tavily.com/search"

// userAgent is the HTTP User-Agent sent to Tavily (§12).
const userAgent = "mst/dev"

// SetAPIBase overrides the API base URL for testing. Callers must restore the
// original value after the test (typically via defer).
func SetAPIBase(base string) { apiBase = base }

// APIBase returns the current API base URL.
func APIBase() string { return apiBase }

// tavily is the Tavily Search engine implementation.
type tavily struct{}

// New returns a new Tavily Search engine. There is exactly one instance —
// tavily is stateless (no per-query state, no connection pooling beyond stdlib).
func New() engine.Engine { return &tavily{} }

// Name implements engine.Engine.
func (*tavily) Name() string { return "tavily" }

// Descriptor implements engine.Engine.
func (*tavily) Descriptor() engine.EngineInfo {
	return engine.EngineInfo{
		Name:          "tavily",
		Keyed:         true,
		KeyEnv:        "TAVILY_API_KEY",
		DefaultWeight: 1.2,
		Enabled:       false, // opt-in (Tier D, AI-oriented)
		Category:      "general",
	}
}

// Search implements engine.Engine. It sends a POST request to the Tavily Search
// API and parses the response into []engine.Result. On any error (HTTP status
// >= 400, parse failure, context cancellation) it returns nil results and a
// non-nil error. The dispatcher wraps this into EngineRun with timing metadata.
//
// Retry policy (§11): delegated to internal/engine/retry — up to 2 retries on
// HTTP 429/5xx and network errors, exponential backoff (base 500ms, factor 2),
// ±20% jitter, honoring Retry-After.
//
// Engine options (opts):
//   - IncludeAnswer: request AI-generated answer in the response (default false)
//   - SearchDepth: "basic" (default) or "advanced"
func (t *tavily) Search(ctx context.Context, q string, opts engine.Options) ([]engine.Result, error) {
	results, _, err := t.fetch(ctx, q, opts)
	return results, err
}

// SearchWithRaw implements engine.RawEngine (§10 --raw): like Search but also
// returns the decoded native JSON response (all fields, not just those mst
// maps) so --raw shows exactly what Tavily returned before normalization.
func (t *tavily) SearchWithRaw(ctx context.Context, q string, opts engine.Options) ([]engine.Result, any, error) {
	return t.fetch(ctx, q, opts)
}

// fetch is the shared Tavily request+parse core for Search and SearchWithRaw. It
// returns the normalized results plus the decoded native response (raw) for
// --raw; raw is nil on any pre-parse error path.
func (t *tavily) fetch(ctx context.Context, q string, opts engine.Options) ([]engine.Result, any, error) {
	apiKey := os.Getenv("TAVILY_API_KEY")
	if apiKey == "" {
		return nil, nil, fmt.Errorf("tavily: TAVILY_API_KEY not set")
	}

	reqURL, err := buildRequestURL(opts.Limit)
	if err != nil {
		return nil, nil, fmt.Errorf("tavily: build request: %w", err)
	}

	// Build the JSON request body with optional parameters.
	bodyMap := map[string]any{
		"api_key":        apiKey,
		"query":          q,
		"max_results":    limitCap(opts.Limit),
		"include_answer": opts.IncludeAnswer,
	}
	if opts.SearchDepth != "" {
		bodyMap["search_depth"] = opts.SearchDepth
	}

	body, err := json.Marshal(bodyMap)
	if err != nil {
		return nil, nil, fmt.Errorf("tavily: marshal request: %w", err)
	}

	// newReq builds a fresh request per retry attempt.
	newReq := func() (*http.Request, error) {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Accept", "application/json")
		httpReq.Header.Set("User-Agent", userAgent)
		return httpReq, nil
	}

	resp, err := retry.Do(ctx, "tavily", newReq)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("tavily: read body: %w", err)
	}

	var apiResp apiResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, nil, fmt.Errorf("tavily: decode response: %w", err)
	}

	if apiResp.Error != nil {
		return nil, nil, fmt.Errorf("tavily: API error: %s", apiResp.Error.Message)
	}

	// Decode the native body generically for --raw (all fields, full fidelity).
	// Best-effort: if it fails (it should not, since the typed decode succeeded),
	// raw stays nil and --raw simply omits this engine.
	var raw any
	_ = json.Unmarshal(respBody, &raw)

	return parseResults(apiResp), raw, nil
}

// limitCap caps the limit to the Tavily API maximum (50 for free tier).
func limitCap(limit int) int {
	if limit > 50 {
		return 50
	}
	if limit < 1 {
		return 1
	}
	return limit
}

// buildRequestURL constructs the Tavily Search API URL.
func buildRequestURL(_ int) (string, error) {
	base, err := urlParse(apiBase)
	if err != nil {
		return "", err
	}
	return base.String(), nil
}

// urlParse is a var for test override.
var urlParse = urlParsed

func urlParsed(s string) (*url.URL, error) { return url.Parse(s) }

// --- Tavily API response types (only the fields we need) -----------------

// apiResponse is a minimal representation of the Tavily Search API JSON body.
type apiResponse struct {
	Results []apiResult `json:"results"`
	Error   *apiError   `json:"error,omitempty"`
	Answer  string      `json:"answer,omitempty"`
}

// apiError is an error response from the Tavily API.
type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// apiResult is one item in the Tavily Search API results array.
type apiResult struct {
	URL       string  `json:"url"`
	Title     string  `json:"title"`
	Content   string  `json:"content"`
	Score     float64 `json:"score,omitempty"`
	GroupName string  `json:"group_name,omitempty"`
}

// htmlTagRe strips HTML tags from text (§5).
var htmlTagRe = regexp.MustCompile(`<[^>]*>`)

// parseResults maps Tavily API results into engine.Result slices. It is a pure
// function — no I/O, no side effects — making it ideal for golden-table tests.
func parseResults(resp apiResponse) []engine.Result {
	results := make([]engine.Result, 0, len(resp.Results))
	for _, r := range resp.Results {
		if r.URL == "" {
			continue // skip entries without a URL
		}
		results = append(results, engine.Result{
			Title:   strings.TrimSpace(r.Title),
			URL:     r.URL,
			Snippet: htmlTagRe.ReplaceAllString(strings.TrimSpace(r.Content), ""),
		})
	}
	return results
}

// GetAnswerFromRaw extracts the answer string from a tavily raw payload.
// It returns the answer if present, or empty string if not. Used by the
// format package to surface the AI-generated answer in the output envelope.
func GetAnswerFromRaw(raw any) string {
	if raw == nil {
		return ""
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return ""
	}
	ans, _ := m["answer"].(string)
	return ans
}
