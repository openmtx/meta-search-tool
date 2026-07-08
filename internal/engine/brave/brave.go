// Package brave implements the Brave Search API engine (§5 Tier A).
//
// Brave Search is a structured-API engine: it returns JSON with well-defined
// fields, making parsing reliable and block-resistant. It requires a
// BRAVE_API_KEY environment variable; without it, the engine auto-skips
// (StatusMissingKey) via the registry's key-check in resolvePlan.
//
// The HTTP retry/backoff policy (§11) is shared via internal/engine/retry.
package brave

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
//
// Retry policy (§11): delegated to internal/engine/retry — up to 2 retries on
// HTTP 429/5xx and network errors, exponential backoff (base 500ms, factor 2),
// ±20% jitter, honoring Retry-After.
func (b *brave) Search(ctx context.Context, q string, opts engine.Options) ([]engine.Result, error) {
	results, _, err := b.fetch(ctx, q, opts)
	return results, err
}

// SearchWithRaw implements engine.RawEngine (§10 --raw): like Search but also
// returns the decoded native JSON response (all fields, not just those mst
// maps) so --raw shows exactly what Brave returned before normalization.
func (b *brave) SearchWithRaw(ctx context.Context, q string, opts engine.Options) ([]engine.Result, any, error) {
	return b.fetch(ctx, q, opts)
}

// fetch is the shared Brave request+parse core for Search and SearchWithRaw. It
// returns the normalized results plus the decoded native response (raw) for
// --raw; raw is nil on any pre-parse error path.
func (b *brave) fetch(ctx context.Context, q string, opts engine.Options) ([]engine.Result, any, error) {
	apiKey := os.Getenv("BRAVE_API_KEY")
	if apiKey == "" {
		return nil, nil, fmt.Errorf("brave: BRAVE_API_KEY not set")
	}

	reqURL, err := buildRequestURL(q, opts.Limit)
	if err != nil {
		return nil, nil, fmt.Errorf("brave: build request: %w", err)
	}

	// newReq builds a fresh request per retry attempt (headers are constant).
	newReq := func() (*http.Request, error) {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		if err != nil {
			return nil, err
		}
		httpReq.Header.Set("Accept", "application/json")
		httpReq.Header.Set("X-Subscription-Token", apiKey)
		httpReq.Header.Set("User-Agent", userAgent)
		return httpReq, nil
	}

	resp, err := retry.Do(ctx, "brave", newReq)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("brave: read body: %w", err)
	}

	var apiResp apiResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, nil, fmt.Errorf("brave: decode response: %w", err)
	}

	// Decode the native body generically for --raw (all fields, full fidelity).
	// Best-effort: if it fails (it should not, since the typed decode succeeded),
	// raw stays nil and --raw simply omits this engine.
	var raw any
	_ = json.Unmarshal(body, &raw)

	return parseResults(apiResp), raw, nil
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
