// Package google implements the Google Programmable Search Engine API (§5 Tier C).
//
// Google is a keyed structured-API engine: it returns JSON with well-defined
// fields, making parsing reliable. It requires GOOGLE_API_KEY and GOOGLE_CX
// environment variables; without them the engine auto-skips (StatusMissingKey).
// It is disabled by default — opt in with `--engine google` or config.
//
// The HTTP retry/backoff policy (§11) is shared via internal/engine/retry.
package google

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"

	"github.com/openmtx/meta-search-tool/internal/engine"
	"github.com/openmtx/meta-search-tool/internal/engine/retry"
)

// apiBase is the Google Custom Search JSON API endpoint.
var apiBase = "https://www.googleapis.com/customsearch/v1"

// userAgent is the HTTP User-Agent sent to Google (§12).
const userAgent = "mst/dev"

// SetAPIBase overrides the API base URL for testing. Callers must restore the
// original value after the test (typically via defer).
func SetAPIBase(base string) { apiBase = base }

// APIBase returns the current API base URL.
func APIBase() string { return apiBase }

// google is the Google Custom Search engine implementation.
type google struct{}

// New returns a new Google Custom Search engine. There is exactly one instance
// — google is stateless.
func New() engine.Engine { return &google{} }

// Name implements engine.Engine.
func (*google) Name() string { return "google" }

// Descriptor implements engine.Engine.
func (*google) Descriptor() engine.EngineInfo {
	return engine.EngineInfo{
		Name:          "google",
		Keyed:         true,
		KeyEnv:        "GOOGLE_API_KEY",
		DefaultWeight: 1.0,
		Enabled:       false, // opt-in with keys + CX
		Category:      "general",
	}
}

// Search implements engine.Engine. It sends a GET request to the Google Custom
// Search API and parses the response into []engine.Result. On any error (HTTP
// status >= 400, parse failure, context cancellation) it returns nil results
// and a non-nil error.
//
// Retry policy (§11): delegated to internal/engine/retry — up to 2 retries on
// HTTP 429/5xx and network errors, exponential backoff (base 500ms, factor 2),
// ±20% jitter, honoring Retry-After.
func (g *google) Search(ctx context.Context, q string, opts engine.Options) ([]engine.Result, error) {
	results, _, err := g.fetch(ctx, q, opts)
	return results, err
}

// SearchWithRaw implements engine.RawEngine (§10 --raw).
func (g *google) SearchWithRaw(ctx context.Context, q string, opts engine.Options) ([]engine.Result, any, error) {
	return g.fetch(ctx, q, opts)
}

// fetch is the shared Google request+parse core.
func (g *google) fetch(ctx context.Context, q string, opts engine.Options) ([]engine.Result, any, error) {
	apiKey := os.Getenv("GOOGLE_API_KEY")
	cx := os.Getenv("GOOGLE_CX")
	if apiKey == "" || cx == "" {
		return nil, nil, fmt.Errorf("google: GOOGLE_API_KEY and GOOGLE_CX must both be set")
	}

	reqURL, err := buildRequestURL(q, apiKey, cx, opts.Limit)
	if err != nil {
		return nil, nil, fmt.Errorf("google: build request: %w", err)
	}

	newReq := func() (*http.Request, error) {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		if err != nil {
			return nil, err
		}
		httpReq.Header.Set("Accept", "application/json")
		httpReq.Header.Set("User-Agent", userAgent)
		return httpReq, nil
	}

	resp, err := retry.Do(ctx, "google", newReq)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("google: read body: %w", err)
	}

	var apiResp apiResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, nil, fmt.Errorf("google: decode response: %w", err)
	}

	// Surface API errors (e.g. invalid key/CX, quota exceeded).
	if apiResp.Error != nil {
		return nil, nil, fmt.Errorf("google: API error: %s", apiResp.Error.Message)
	}

	// Decode the native body generically for --raw.
	var raw any
	_ = json.Unmarshal(body, &raw)

	return parseResults(apiResp), raw, nil
}

// buildRequestURL constructs the Google Custom Search API query URL.
// Parameters: key (API key), cx (Programmable Search Engine ID), q (query),
// num (results per page, 1–10).
func buildRequestURL(q, apiKey, cx string, limit int) (string, error) {
	base, err := url.Parse(apiBase)
	if err != nil {
		return "", err
	}
	params := base.Query()
	params.Set("key", apiKey)
	params.Set("cx", cx)
	params.Set("q", q)
	// Google's num param: 1–10. Cap at 10, floor at 1.
	if limit > 10 {
		limit = 10
	}
	if limit < 1 {
		limit = 1
	}
	params.Set("num", fmt.Sprintf("%d", limit))
	base.RawQuery = params.Encode()
	return base.String(), nil
}

// --- Google API response types --------------------------------------------

// apiResponse is the top-level Google Custom Search JSON response.
type apiResponse struct {
	Items []apiItem `json:"items"`
	Error *apiError `json:"error,omitempty"`
}

// apiError is an error response from the Google API.
type apiError struct {
	Message string `json:"message"`
}

// apiItem is one search result from the Google API.
type apiItem struct {
	Title   string `json:"title"`
	Link    string `json:"link"`
	Snippet string `json:"snippet"`
}

// parseResults maps Google API items into engine.Result slices.
func parseResults(resp apiResponse) []engine.Result {
	results := make([]engine.Result, 0, len(resp.Items))
	for _, item := range resp.Items {
		if item.Link == "" {
			continue
		}
		results = append(results, engine.Result{
			Title:   item.Title,
			URL:     item.Link,
			Snippet: item.Snippet,
		})
	}
	return results
}
