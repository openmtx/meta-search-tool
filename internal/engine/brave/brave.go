// Package brave implements the Brave Search API engine (§5 Tier A).
//
// Brave Search is a structured-API engine: it returns JSON with well-defined
// fields, making parsing reliable and block-resistant. It requires a
// BRAVE_API_KEY environment variable; without it, the engine auto-skips
// (StatusMissingKey) via the registry's key-check in resolvePlan.
package brave

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/openmtx/meta-search-tool/internal/engine"
)

// apiBase is the Brave Search API endpoint. Tests override it via SetAPIBase.
var apiBase = "https://api.search.brave.com/res/v1/web/search"

// userAgent is the HTTP User-Agent sent to Brave (§12).
const userAgent = "mst/dev"

// SetAPIBase overrides the API base URL for testing. Callers must restore the
// original value after the test (typically via defer).
func SetAPIBase(base string) { apiBase = base }

// APIBase returns the current API base URL.
func APIBase() string { return apiBase }

// brave is the Brave Search engine implementation.
type brave struct{}

// New returns a new Brave Search engine. There is exactly one instance —
// brave is stateless (no per-query state, no connection pooling beyond stdlib).
func New() engine.Engine { return &brave{} }

// Name implements engine.Engine.
func (*brave) Name() string { return "brave" }

// Descriptor implements engine.Engine.
func (*brave) Descriptor() engine.EngineInfo {
	return engine.EngineInfo{
		Name:          "brave",
		Keyed:         true,
		KeyEnv:        "BRAVE_API_KEY",
		DefaultWeight: 2.0,
		Enabled:       true,
	}
}

// Search implements engine.Engine. It sends a GET request to the Brave Search
// API and parses the response into []engine.Result. On any error (HTTP status
// >= 400, parse failure, context cancellation) it returns nil results and a
// non-nil error. The dispatcher wraps this into EngineRun with timing metadata.
func (*brave) Search(ctx context.Context, q string, opts engine.Options) ([]engine.Result, error) {
	apiKey := os.Getenv("BRAVE_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("brave: BRAVE_API_KEY not set")
	}

	reqURL, err := buildRequestURL(q, opts.Limit)
	if err != nil {
		return nil, fmt.Errorf("brave: build request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("brave: request: %w", err)
	}
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("X-Subscription-Token", apiKey)
	httpReq.Header.Set("User-Agent", userAgent)

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("brave: request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("brave: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var apiResp apiResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("brave: decode response: %w", err)
	}

	return parseResults(apiResp), nil
}

// buildRequestURL constructs the Brave Search API query URL with parameters.
func buildRequestURL(q string, limit int) (string, error) {
	base, err := url.Parse(apiBase)
	if err != nil {
		return "", err
	}
	params := base.Query()
	params.Set("q", q)
	// Brave API uses 'count' for result count; max is 20 per page.
	if limit > 20 {
		limit = 20
	}
	if limit < 1 {
		limit = 1
	}
	params.Set("count", fmt.Sprintf("%d", limit))
	base.RawQuery = params.Encode()
	return base.String(), nil
}

// --- Brave API response types (only the fields we need) -------------------

// apiResponse is a minimal representation of the Brave Search API JSON body.
// We only deserialize the fields used by mst; unknown fields are ignored by
// the JSON decoder (the default behavior).
type apiResponse struct {
	Web webResponse `json:"web"`
}

// webResponse holds the top-level results array from the Brave API.
type webResponse struct {
	Results []apiResult `json:"results"`
}

// apiResult is one item in the Brave Search API results array. The field names
// and types match the Brave Search API v1 schema.
type apiResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

// parseResults maps Brave API results into engine.Result slices. It is a pure
// function — no I/O, no side effects — making it ideal for golden-table tests.
func parseResults(resp apiResponse) []engine.Result {
	results := make([]engine.Result, 0, len(resp.Web.Results))
	for _, r := range resp.Web.Results {
		if r.URL == "" {
			continue // skip entries without a URL
		}
		results = append(results, engine.Result{
			Title:   r.Title,
			URL:     r.URL,
			Snippet: r.Snippet,
		})
	}
	return results
}
