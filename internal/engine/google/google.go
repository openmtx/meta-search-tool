// Package google implements Google search (§5 Tier C) with two modes:
//
//  1. Official API (keyed):       when GOOGLE_API_KEY + GOOGLE_CX are set
//  2. Public CSE token (keyless): when no API key but a CX is available
//     (default CX: blackle.com's public CSE)
//
// Mode precedence (first wins): official API > public CSE. The public CSE mode
// follows SearXNG's approach: fetches a session token from
// google.com/cse/cse.js?cx=<CX> and queries cse.google.com/cse/element/v1.
// The old HTML scrape fallback was retired in v0.5: Google now requires
// JavaScript/TLS-impersonation for the normal web endpoint, and upstream
// SearXNG moved to a WML/Nokia-UA path that Go's standard TLS stack cannot use
// reliably. CSE is the supported keyless path.
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
	"strings"
	"sync"
	"time"

	"github.com/openmtx/meta-search-tool/internal/engine"
	"github.com/openmtx/meta-search-tool/internal/engine/retry"
)

// ---- Configurable endpoints (tests override via Set*) -----------------------

// apiBase is the Google Custom Search JSON API endpoint.
var apiBase = "https://www.googleapis.com/customsearch/v1"

// cseTokenBase is the Google CSE JS endpoint from which we obtain a session token.
var cseTokenBase = "https://www.google.com/cse/cse.js"

// cseSearchBase is the Google CSE element API endpoint.
var cseSearchBase = "https://cse.google.com/cse/element/v1"

// defaultPublicCX is the default public CSE ID used for keyless Google search.
// This is blackle.com's public Programmable Search Engine, same as SearXNG uses.
var defaultPublicCX = "partner-pub-8993703457585266:4862972284"

// SetAPIBase overrides the API base URL for testing.
func SetAPIBase(base string) { apiBase = base }

// APIBase returns the current API base URL.
func APIBase() string { return apiBase }

// SetCSETokenBase overrides the CSE token URL for testing.
func SetCSETokenBase(base string) { cseTokenBase = base }

// CSETokenBase returns the current CSE token URL.
func CSETokenBase() string { return cseTokenBase }

// SetCSESearchBase overrides the CSE search URL for testing.
func SetCSESearchBase(base string) { cseSearchBase = base }

// CSESearchBase returns the current CSE search URL.
func CSESearchBase() string { return cseSearchBase }

// SetDefaultPublicCX overrides the default public CX for testing.
func SetDefaultPublicCX(cx string) { defaultPublicCX = cx }

// DefaultPublicCX returns the current default public CX.
func DefaultPublicCX() string { return defaultPublicCX }

// google is the Google search engine implementation (hybrid API + scrape).
type google struct{}

// New returns a new Google search engine.
func New() engine.Engine { return &google{} }

// Name implements engine.Engine.
func (*google) Name() string { return "google" }

// Descriptor implements engine.Engine. The engine is effectively keyless — if
// GOOGLE_API_KEY + GOOGLE_CX are set, it uses the official JSON API; otherwise
// it uses the public CSE token approach (SearXNG-style, keyless).
// Enabled by default in the general category (after ddg in registration order).
func (*google) Descriptor() engine.EngineInfo {
	return engine.EngineInfo{
		Name:          "google",
		Keyed:         false, // works without keys (public CSE)
		KeyEnv:        "",
		DefaultWeight: 1.0,
		Enabled:       true,
		Category:      "general",
	}
}

// Search implements engine.Engine.
func (g *google) Search(ctx context.Context, q string, opts engine.Options) ([]engine.Result, error) {
	results, _, err := g.fetch(ctx, q, opts)
	return results, err
}

// SearchWithRaw implements engine.RawEngine (§10 --raw). The raw payload is the
// parsed native response (a map[string]any for both API and CSE modes).
func (g *google) SearchWithRaw(ctx context.Context, q string, opts engine.Options) ([]engine.Result, any, error) {
	return g.fetch(ctx, q, opts)
}

// effectiveCX returns the CSE ID to use: GOOGLE_CX env var if set, otherwise
// the built-in default public CX (blackle.com's public CSE, same as SearXNG).
func effectiveCX() string {
	if cx := os.Getenv("GOOGLE_CX"); cx != "" {
		return cx
	}
	return defaultPublicCX
}

// fetch chooses the best available mode in order:
//  1. Official API (GOOGLE_API_KEY + CX)
//  2. Public CSE token (CX available from env or built-in default)
func (g *google) fetch(ctx context.Context, q string, opts engine.Options) ([]engine.Result, any, error) {
	apiKey := os.Getenv("GOOGLE_API_KEY")
	cx := effectiveCX()

	// Mode 1: Official REST API (needs both key and CX).
	if apiKey != "" && os.Getenv("GOOGLE_CX") != "" {
		return g.fetchAPI(ctx, q, apiKey, cx, opts)
	}

	// Mode 2: Public CSE token approach (needs only a CX, no API key).
	if cx != "" {
		return g.fetchPublicCSE(ctx, q, cx, opts)
	}

	return nil, nil, fmt.Errorf("google: no usable mode (set GOOGLE_API_KEY+GOOGLE_CX or configure a CSE id)")
}

// ---- Official API path -----------------------------------------------------

func (g *google) fetchAPI(ctx context.Context, q string, apiKey, cx string, opts engine.Options) ([]engine.Result, any, error) {
	reqURL, err := buildAPIURL(q, apiKey, cx, opts.Limit)
	if err != nil {
		return nil, nil, fmt.Errorf("google: build request: %w", err)
	}

	newReq := func() (*http.Request, error) {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		if err != nil {
			return nil, err
		}
		httpReq.Header.Set("Accept", "application/json")
		httpReq.Header.Set("User-Agent", engine.UserAgent())
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
	if apiResp.Error != nil {
		return nil, nil, fmt.Errorf("google: API error: %s", apiResp.Error.Message)
	}

	var raw any
	_ = json.Unmarshal(body, &raw)

	return parseAPIResults(apiResp), raw, nil
}

// buildAPIURL constructs the Google Custom Search API URL with key/cx/q/num.
func buildAPIURL(q, apiKey, cx string, limit int) (string, error) {
	base, err := url.Parse(apiBase)
	if err != nil {
		return "", err
	}
	params := base.Query()
	params.Set("key", apiKey)
	params.Set("cx", cx)
	params.Set("q", q)
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

// ---- Public CSE token mode (SearXNG-style, keyless) ------------------------

// cseTokenCache holds a cached CSE session token with expiry.
type cseTokenCache struct {
	mu      sync.Mutex
	token   string
	cselibv string
	exp     string
	expiry  time.Time
}

var cseCache cseTokenCache

// getCSEToken returns a valid CSE session token, fetching a fresh one if
// the cache is empty or expired. Follows SearXNG's approach: GET
// google.com/cse/cse.js?cx=<CX> and parse the JSONP response for cse_token.
func getCSEToken(ctx context.Context, cx string) (token, cselibv, exp string, _ error) {
	cseCache.mu.Lock()
	defer cseCache.mu.Unlock()

	if time.Now().Before(cseCache.expiry) && cseCache.token != "" {
		return cseCache.token, cseCache.cselibv, cseCache.exp, nil
	}

	token, cselibv, exp, err := fetchCSEToken(ctx, cx)
	if err != nil {
		return "", "", "", err
	}

	cseCache.token = token
	cseCache.cselibv = cselibv
	cseCache.exp = exp
	cseCache.expiry = time.Now().Add(1 * time.Hour)
	return token, cselibv, exp, nil
}

// ResetCSETokenCache clears the cached CSE token (for testing).
func ResetCSETokenCache() {
	cseCache.mu.Lock()
	defer cseCache.mu.Unlock()
	cseCache.token = ""
	cseCache.expiry = time.Time{}
}

// fetchCSEToken performs the actual HTTP request to get a CSE token from Google.
func fetchCSEToken(ctx context.Context, cx string) (token, cselibv, exp string, _ error) {
	tokenURL := cseTokenBase + "?cx=" + url.QueryEscape(cx)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tokenURL, nil)
	if err != nil {
		return "", "", "", fmt.Errorf("google: cse token request: %w", err)
	}
	req.Header.Set("User-Agent", engine.UserAgent())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", "", fmt.Errorf("google: cse token fetch: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", "", fmt.Errorf("google: cse token read: %w", err)
	}

	// Response is JSONP like: window.__gcse || ...({cse_token:"...",cselibVersion:"...",exp:["..."]});
	// Extract the JSON object between ({ and });
	text := string(body)
	start := strings.LastIndex(text, "({")
	end := strings.LastIndex(text, "});")
	if start < 0 || end < 0 || end <= start+1 {
		return "", "", "", fmt.Errorf("google: cse token: unexpected response format")
	}

	var tokenData struct {
		CSEToken      string   `json:"cse_token"`
		CSELibVersion string   `json:"cselibVersion"`
		Exp           []string `json:"exp"`
	}
	if err := json.Unmarshal([]byte(text[start+1:end+1]), &tokenData); err != nil {
		return "", "", "", fmt.Errorf("google: cse token parse: %w", err)
	}
	if tokenData.CSEToken == "" {
		return "", "", "", fmt.Errorf("google: cse token: empty token")
	}

	expStr := strings.Join(tokenData.Exp, ",")
	return tokenData.CSEToken, tokenData.CSELibVersion, expStr, nil
}

func (g *google) fetchPublicCSE(ctx context.Context, q string, cx string, opts engine.Options) ([]engine.Result, any, error) {
	token, cselibv, exp, err := getCSEToken(ctx, cx)
	if err != nil {
		return nil, nil, err
	}

	reqURL, err := buildCSESearchURL(q, cx, token, cselibv, exp, opts.Limit)
	if err != nil {
		return nil, nil, fmt.Errorf("google: cse search build: %w", err)
	}

	newReq := func() (*http.Request, error) {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		if err != nil {
			return nil, err
		}
		httpReq.Header.Set("User-Agent", engine.UserAgent())
		httpReq.Header.Set("Referer", "https://cse.google.com/")
		return httpReq, nil
	}

	resp, err := retry.Do(ctx, "google", newReq)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("google: cse search read: %w", err)
	}

	// Response is JSONP: _(<json>). Strip the callback wrapper.
	jsonStr := stripJSONP(string(body))

	var cseResp cseResponse
	if err := json.Unmarshal([]byte(jsonStr), &cseResp); err != nil {
		return nil, nil, fmt.Errorf("google: cse search decode: %w", err)
	}

	if cseResp.Error != nil {
		return nil, nil, fmt.Errorf("google: cse search error: %s", cseResp.Error.Message)
	}

	// Decode the native body generically for --raw.
	var raw any
	_ = json.Unmarshal([]byte(jsonStr), &raw)

	return parseCSEResults(cseResp), raw, nil
}

// stripJSONP removes the JSONP callback wrapper, returning just the JSON body.
// Expected format: _({...}) or similar callback({...}).
func stripJSONP(text string) string {
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start < 0 || end < 0 || end < start {
		return text
	}
	return text[start : end+1]
}

// buildCSESearchURL constructs the CSE element v1 API URL.
func buildCSESearchURL(q, cx, token, cselibv, exp string, limit int) (string, error) {
	base, err := url.Parse(cseSearchBase)
	if err != nil {
		return "", err
	}
	params := base.Query()
	params.Set("rsz", "filtered_cse")
	params.Set("num", "20") // page size, fixed at 20 per SearXNG
	params.Set("hl", "en")
	params.Set("cselibv", cselibv)
	params.Set("cx", cx)
	params.Set("q", q)
	params.Set("safe", "medium")
	params.Set("cse_tok", token)
	params.Set("callback", "_")
	params.Set("rurl", "")
	params.Set("searchtype", "")
	if exp != "" {
		params.Set("exp", exp)
	}
	base.RawQuery = params.Encode()
	return base.String(), nil
}

// cseResponse is the Google CSE element API JSON response.
type cseResponse struct {
	Results []cseResult  `json:"results"`
	Error   *cseAPIError `json:"error,omitempty"`
}

type cseResult struct {
	UnescapedURL        string `json:"unescapedUrl"`
	TitleNoFormatting   string `json:"titleNoFormatting"`
	ContentNoFormatting string `json:"contentNoFormatting"`
}

type cseAPIError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// parseCSEResults maps CSE results into engine.Result slices.
func parseCSEResults(resp cseResponse) []engine.Result {
	results := make([]engine.Result, 0, len(resp.Results))
	for _, r := range resp.Results {
		if r.UnescapedURL == "" {
			continue
		}
		results = append(results, engine.Result{
			Title:   r.TitleNoFormatting,
			URL:     r.UnescapedURL,
			Snippet: r.ContentNoFormatting,
		})
	}
	return results
}

// ---- API response types and parser -----------------------------------------

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

// parseAPIResults maps Google API items into engine.Result slices.
func parseAPIResults(resp apiResponse) []engine.Result {
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
