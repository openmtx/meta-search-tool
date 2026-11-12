// Package bing implements the Bing Web Search v7 API engine (§5 Tier C).
//
// Bing is a structured-API engine: it returns JSON with well-defined fields,
// making parsing reliable. It requires a BING_API_KEY environment variable;
// without it, the engine auto-skips (StatusMissingKey) via the registry's
// key-check in resolvePlan.
//
// The HTTP retry/backoff policy (§11) is shared via internal/engine/retry.
package bing

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
	"github.com/openmtx/meta-search-tool/internal/engine/retry"
)

// apiBase is the Bing Web Search v7 API endpoint. Tests override it via SetAPIBase.
var apiBase = "https://api.bing.microsoft.com/v7.0/search"

// SetAPIBase overrides the API base URL for testing. Callers must restore the
// original value after the test (typically via defer).
func SetAPIBase(base string) { apiBase = base }

// APIBase returns the current API base URL.
func APIBase() string { return apiBase }

// bing is the Bing Web Search engine implementation.
type bing struct{}

// New returns a new Bing Web Search engine. There is exactly one instance —
// bing is stateless (no per-query state, no connection pooling beyond stdlib).
func New() engine.Engine { return &bing{} }

// Name implements engine.Engine.
func (*bing) Name() string { return "bing" }

// Descriptor implements engine.Engine.
func (*bing) Descriptor() engine.EngineInfo {
	return engine.EngineInfo{
		Name:          "bing",
		Keyed:         true,
		KeyEnv:        "BING_API_KEY",
		DefaultWeight: 1.0,
		Enabled:       false, // opt-in (Tier C, keyed)
		Category:      "general",
	}
}

// Search implements engine.Engine. It sends a GET request to the Bing Web
// Search v7 API and parses the response into []engine.Result. On any error
// (HTTP status >= 400, parse failure, context cancellation) it returns nil
// results and a non-nil error. The dispatcher wraps this into EngineRun with
// timing metadata.
//
// Retry policy (§11): delegated to internal/engine/retry — up to 2 retries on
// HTTP 429/5xx and network errors, exponential backoff (base 500ms, factor 2),
// ±20% jitter, honoring Retry-After.
func (b *bing) Search(ctx context.Context, q string, opts engine.Options) ([]engine.Result, error) {
	results, _, err := b.fetch(ctx, q, opts)
	return results, err
}

// SearchWithRaw implements engine.RawEngine (§10 --raw): like Search but also
// returns the decoded native JSON response (all fields, not just those mst
// maps) so --raw shows exactly what Bing returned before normalization.
func (b *bing) SearchWithRaw(ctx context.Context, q string, opts engine.Options) ([]engine.Result, any, error) {
	return b.fetch(ctx, q, opts)
}

// fetch is the shared Bing request+parse core for Search and SearchWithRaw. It
// returns the normalized results plus the decoded native response (raw) for
// --raw; raw is nil on any pre-parse error path.
func (b *bing) fetch(ctx context.Context, q string, opts engine.Options) ([]engine.Result, any, error) {
	apiKey := os.Getenv("BING_API_KEY")
	if apiKey == "" {
		return nil, nil, fmt.Errorf("bing: BING_API_KEY not set")
	}

	reqURL, err := buildRequestURL(q, opts.Limit)
	if err != nil {
		return nil, nil, fmt.Errorf("bing: build request: %w", err)
	}

	// newReq builds a fresh request per retry attempt (headers are constant).
	newReq := func() (*http.Request, error) {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		if err != nil {
			return nil, err
		}
		httpReq.Header.Set("Accept", "application/json")
		httpReq.Header.Set("Ocp-Apim-Subscription-Key", apiKey)
		httpReq.Header.Set("User-Agent", engine.UserAgent())
		return httpReq, nil
	}

	resp, err := retry.Do(ctx, "bing", newReq)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("bing: read body: %w", err)
	}

	var apiResp apiResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, nil, fmt.Errorf("bing: decode response: %w", err)
	}

	if apiResp.Error != nil {
		return nil, nil, fmt.Errorf("bing: API error: %s", apiResp.Error.Message)
	}

	// Decode the native body generically for --raw (all fields, full fidelity).
	// Best-effort: if it fails (it should not, since the typed decode succeeded),
	// raw stays nil and --raw simply omits this engine.
	var raw any
	_ = json.Unmarshal(body, &raw)

	return parseResults(apiResp), raw, nil
}

// buildRequestURL constructs the Bing Web Search API query URL with parameters.
func buildRequestURL(q string, limit int) (string, error) {
	base, err := url.Parse(apiBase)
	if err != nil {
		return "", err
	}
	params := base.Query()
	params.Set("q", q)
	// Bing API uses 'count' for result count; max is 50 per page.
	if limit > 50 {
		limit = 50
	}
	if limit < 1 {
		limit = 1
	}
	params.Set("count", fmt.Sprintf("%d", limit))
	base.RawQuery = params.Encode()
	return base.String(), nil
}

// --- Bing API response types (only the fields we need) -------------------

// apiResponse is a minimal representation of the Bing Web Search v7 JSON body.
// We only deserialize the fields used by mst; unknown fields are ignored by
// the JSON decoder (the default behavior).
type apiResponse struct {
	WebPages *webPages `json:"webPages,omitempty"`
	Error    *apiError `json:"error,omitempty"`
}

// webPages holds the top-level results array from the Bing API.
type webPages struct {
	Results []apiResult `json:"value"`
}

// apiError is an error response from the Bing API.
type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// apiResult is one item in the Bing Web Search API results array. The field
// names and types match the Bing Web Search v7 schema.
type apiResult struct {
	URL         string `json:"url"`
	DisplayName string `json:"displayName"`
	Snippet     string `json:"snippet"`
}

// parseResults maps Bing API results into engine.Result slices. It is a pure
// function — no I/O, no side effects — making it ideal for golden-table tests.
func parseResults(resp apiResponse) []engine.Result {
	if resp.WebPages == nil {
		return nil
	}
	results := make([]engine.Result, 0, len(resp.WebPages.Results))
	for _, r := range resp.WebPages.Results {
		if r.URL == "" {
			continue // skip entries without a URL
		}
		results = append(results, engine.Result{
			Title:   strings.TrimSpace(r.DisplayName),
			URL:     r.URL,
			Snippet: strings.TrimSpace(r.Snippet),
		})
	}
	return results
}
