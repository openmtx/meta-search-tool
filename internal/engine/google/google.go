// Package google implements Google search (§5 Tier C) with triple-mode:
//
//  1. Official API (keyed):         when GOOGLE_API_KEY + GOOGLE_CX are set
//  2. Public CSE token (keyless):   when no API key but a CX is available
//     (default CX: blackle.com's public CSE)
//  3. HTML scrape (keyless):        SearXNG mechanism, fallback when no CX
//
// Mode precedence (first wins): official API > public CSE > HTML scrape.
// The public CSE mode follows SearXNG's approach: fetches a session token from
// google.com/cse/cse.js?cx=<CX> and queries cse.google.com/cse/element/v1.
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

	"github.com/PuerkitoBio/goquery"
	"github.com/openmtx/meta-search-tool/internal/engine"
	"github.com/openmtx/meta-search-tool/internal/engine/retry"
)

// ---- Configurable endpoints (tests override via Set*) -----------------------

// apiBase is the Google Custom Search JSON API endpoint.
var apiBase = "https://www.googleapis.com/customsearch/v1"

// scrapeBase is the Google HTML search endpoint for scrape fallback.
var scrapeBase = "https://www.google.com/search"

// cseTokenBase is the Google CSE JS endpoint from which we obtain a session token.
var cseTokenBase = "https://www.google.com/cse/cse.js"

// cseSearchBase is the Google CSE element API endpoint.
var cseSearchBase = "https://cse.google.com/cse/element/v1"

// defaultPublicCX is the default public CSE ID used for keyless Google search.
// This is blackle.com's public Programmable Search Engine, same as SearXNG uses.
var defaultPublicCX = "partner-pub-8993703457585266:4862972284"

// userAgent is the HTTP User-Agent sent to Google (§12).
const userAgent = "mst/dev"

// scrapeUA is a realistic browser UA for the scrape fallback (§12 carve-out).
const scrapeUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36"

// SetAPIBase overrides the API base URL for testing.
func SetAPIBase(base string) { apiBase = base }

// APIBase returns the current API base URL.
func APIBase() string { return apiBase }

// SetScrapeBase overrides the scrape base URL for testing.
func SetScrapeBase(base string) { scrapeBase = base }

// ScrapeBase returns the current scrape base URL.
func ScrapeBase() string { return scrapeBase }

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
// GOOGLE_API_KEY + GOOGLE_CX are set, it uses the official JSON API; if only
// a CX is available (env var or built-in default), it uses the public CSE token
// approach (SearXNG-style); otherwise it falls back to HTML scrape. Disabled
// by default.
func (*google) Descriptor() engine.EngineInfo {
	return engine.EngineInfo{
		Name:          "google",
		Keyed:         false, // works without keys (public CSE or scrape)
		KeyEnv:        "",
		DefaultWeight: 1.0,
		Enabled:       false, // opt-in
		Category:      "general",
	}
}

// Search implements engine.Engine.
func (g *google) Search(ctx context.Context, q string, opts engine.Options) ([]engine.Result, error) {
	results, _, err := g.fetch(ctx, q, opts)
	return results, err
}

// SearchWithRaw implements engine.RawEngine (§10 --raw). The raw payload is the
// parsed native response: for API mode a map[string]any, for scrape mode a
// string (HTML body).
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
//  3. HTML scrape (fallback)
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

	// Mode 3: HTML scrape fallback.
	return g.fetchScrape(ctx, q, opts)
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
	req.Header.Set("User-Agent", userAgent)

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
		httpReq.Header.Set("User-Agent", userAgent)
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

// ---- HTML scrape fallback (SearXNG mechanism) ------------------------------

// scrapePageSize is the number of results per scrape page (Google shows ~10).
const scrapePageSize = 10

func (g *google) fetchScrape(ctx context.Context, q string, opts engine.Options) ([]engine.Result, any, error) {
	var allResults []engine.Result
	pageno := 1

	for {
		if err := ctx.Err(); err != nil {
			if len(allResults) > 0 {
				return allResults, nil, err
			}
			return nil, nil, err
		}
		if len(allResults) >= opts.Limit {
			break
		}

		start := (pageno - 1) * scrapePageSize
		reqURL, err := buildScrapeURL(q, start)
		if err != nil {
			if len(allResults) > 0 {
				return allResults, nil, fmt.Errorf("google: build scrape URL: %w", err)
			}
			return nil, nil, fmt.Errorf("google: build scrape URL: %w", err)
		}

		newReq := func() (*http.Request, error) {
			httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
			if err != nil {
				return nil, err
			}
			httpReq.Header.Set("User-Agent", scrapeUA)
			httpReq.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
			httpReq.Header.Set("Accept-Language", "en-US,en;q=0.9")
			// Bypass Google's GDPR consent dialog.
			httpReq.Header.Set("Cookie", "CONSENT=YES+")
			return httpReq, nil
		}

		resp, err := retry.Do(ctx, "google", newReq)
		if err != nil {
			if len(allResults) > 0 {
				return allResults, nil, err
			}
			return nil, nil, err
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			if len(allResults) > 0 {
				return allResults, nil, fmt.Errorf("google: read body: %w", err)
			}
			return nil, nil, fmt.Errorf("google: read body: %w", err)
		}

		bodyStr := string(body)

		// Detect Google's bot-protection CAPTCHA/sorry page.
		if isGoogleSorry(bodyStr) {
			if len(allResults) > 0 {
				return allResults, bodyStr, errGoogleCaptcha
			}
			return nil, bodyStr, errGoogleCaptcha
		}

		results := parseScrapeResults(bodyStr)
		allResults = append(allResults, results...)

		if len(results) == 0 {
			break // empty page → no more results
		}
		pageno++
	}

	if len(allResults) > opts.Limit {
		allResults = allResults[:opts.Limit]
	}
	if len(allResults) == 0 {
		return nil, nil, nil
	}
	return allResults, nil, nil
}

// errGoogleCaptcha is returned when Google responds with a CAPTCHA/bot-blocker.
var errGoogleCaptcha = fmt.Errorf("google: CAPTCHA/sorry page (bot detection triggered)")

// isGoogleSorry detects Google's bot-blocking responses (SearXNG's
// detect_google_sorry). Google blocks via redirect to sorry.google.com or short
// HTML with "/sorry/" in the text.
func isGoogleSorry(body string) bool {
	if len(body) < 2000 && strings.Contains(body, "/sorry/") {
		return true
	}
	if strings.Contains(body, "sorry.google.com") {
		return true
	}
	return false
}

// buildScrapeURL constructs the Google HTML search URL for scraping.
// Parameters follow SearXNG's google.py: q, start, filter=0.
func buildScrapeURL(q string, start int) (string, error) {
	base, err := url.Parse(scrapeBase)
	if err != nil {
		return "", err
	}
	params := base.Query()
	params.Set("q", q)
	params.Set("start", fmt.Sprintf("%d", start))
	params.Set("filter", "0") // no duplicate filtering
	base.RawQuery = params.Encode()
	return base.String(), nil
}

// parseScrapeResults extracts results from Google's HTML search page using
// the SearXNG-inspired selector approach. It looks for result links (href
// starting with /url?q=) and extracts title + snippet from surrounding markup.
func parseScrapeResults(htmlStr string) []engine.Result {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlStr))
	if err != nil {
		return nil
	}

	var results []engine.Result

	// Google result links: <a href="/url?q=REAL_URL&sa=U&...">TITLE</a>
	// These are inside result containers. We find links whose href starts with
	// /url?q= and extract the real URL, title, and surrounding snippet.
	doc.Find("a[href^='/url?q=']").Each(func(_ int, sel *goquery.Selection) {
		href, exists := sel.Attr("href")
		if !exists || href == "" {
			return
		}

		u := unwrapGoogleURL(href)
		if u == "" {
			return
		}

		title := strings.TrimSpace(sel.Text())
		if title == "" {
			return
		}

		// Try to find the snippet: traverse up to a common ancestor then look
		// for snippet-like divs. SearXNG uses a complex tree traversal; we use
		// a simpler approach: look for a sibling/ancestor div containing text.
		snippet := findSnippet(sel)

		results = append(results, engine.Result{
			Title:   title,
			URL:     u,
			Snippet: snippet,
		})
	})

	// Dedup by URL (Google sometimes returns duplicates on multi-page).
	seen := make(map[string]bool)
	deduped := results[:0]
	for _, r := range results {
		if !seen[r.URL] {
			seen[r.URL] = true
			deduped = append(deduped, r)
		}
	}

	return deduped
}

// unwrapGoogleURL extracts the real URL from Google's /url?q=... redirect,
// following SearXNG's pattern: /url?q=REAL_URL&sa=U...
func unwrapGoogleURL(href string) string {
	if !strings.HasPrefix(href, "/url?q=") {
		return href
	}
	// Extract between /url?q= and &sa=U...
	rest := href[7:] // after "/url?q="
	if idx := strings.Index(rest, "&sa="); idx >= 0 {
		rest = rest[:idx]
	}
	decoded, err := url.QueryUnescape(rest)
	if err != nil {
		return rest
	}
	return decoded
}

// findSnippet tries to locate the snippet text near a result link. Google's
// HTML structure varies; we traverse upward from the link to find a candidate
// snippet container.
func findSnippet(sel *goquery.Selection) string {
	// Try common snippet patterns in Google's HTML.
	// Pattern 1: the link's grandparent div contains a snippet span/div.
	snippet := sel.ParentsUntil("~").Find("span.aCOpRe").First().Text()
	if snippet != "" {
		return strings.TrimSpace(snippet)
	}

	// Pattern 2: look for div[data-sncf] near the link.
	snippet = sel.ParentsFiltered("div.g").Find("div[data-sncf]").First().Text()
	if snippet != "" {
		return stripNestedScripts(snippet)
	}

	// Pattern 3: traverse up and look for a div with a style-constrained text
	// block (SearXNG's approach: div[@style] near the result).
	sel.ParentsFiltered("div.g").Find("div").EachWithBreak(func(_ int, d *goquery.Selection) bool {
		txt := strings.TrimSpace(d.Text())
		if len(txt) > 50 && !strings.Contains(txt, ".com") {
			snippet = txt
			return false // stop
		}
		return true
	})

	return stripNestedScripts(snippet)
}

// stripNestedScripts removes <script> content from an HTML fragment.
func stripNestedScripts(s string) string {
	// Simple cleanup: if the text contains HTML tags, remove them.
	if strings.Contains(s, "<") && strings.Contains(s, ">") {
		doc, err := goquery.NewDocumentFromReader(strings.NewReader(s))
		if err == nil {
			return strings.TrimSpace(doc.Text())
		}
	}
	return strings.TrimSpace(s)
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
