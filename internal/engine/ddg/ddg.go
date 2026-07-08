// Package ddg implements the DuckDuckGo HTML scrape engine (§5 Tier C).
//
// DuckDuckGo is a keyless HTML scrape engine. It sends a GET request to
// html.duckduckgo.com and parses the HTML response using goquery. It requires
// no API key — StatusReady at all times (unless the site is unreachable).
// DDG is **off by default** (§17.3) because datacenter requests get heavily
// anti-scraped; opt in with `--engine ddg` or `ddg.enabled: true`.
//
// The HTTP retry/backoff policy (§11) is shared via internal/engine/retry.
package ddg

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/openmtx/meta-search-tool/internal/engine"
	"github.com/openmtx/meta-search-tool/internal/engine/retry"
)

// scrapeBase is the DuckDuckGo HTML search URL. Tests override it via SetScrapeBase.
var scrapeBase = "https://html.duckduckgo.com/html/"

// userAgent is the HTTP User-Agent sent to DuckDuckGo (§12).
const userAgent = "mst/dev"

// SetScrapeBase overrides the scrape base URL for testing. Callers must restore
// the original value after the test (typically via defer).
func SetScrapeBase(base string) { scrapeBase = base }

// ScrapeBase returns the current scrape base URL.
func ScrapeBase() string { return scrapeBase }

// ddg is the DuckDuckGo search engine implementation.
type ddg struct{}

// New returns a new DuckDuckGo engine. There is exactly one instance — ddg is
// stateless (no per-query state, no connection pooling beyond stdlib).
func New() engine.Engine { return &ddg{} }

// Name implements engine.Engine.
func (*ddg) Name() string { return "ddg" }

// Descriptor implements engine.Engine.
func (*ddg) Descriptor() engine.EngineInfo {
	return engine.EngineInfo{
		Name:          "ddg",
		Keyed:         false,
		KeyEnv:        "",
		DefaultWeight: 0.5,
		Enabled:       false, // off by default (§17.3)
	}
}

// Search implements engine.Engine. It sends a GET request to the DuckDuckGo
// HTML search page and parses the results into []engine.Result. On any error
// (HTTP status >= 400, parse failure, context cancellation) it returns nil
// results and a non-nil error. The dispatcher wraps this into EngineRun with
// timing metadata.
//
// Retry policy (§11): delegated to internal/engine/retry — up to 2 retries on
// HTTP 429/5xx and network errors, exponential backoff (base 500ms, factor 2),
// ±20% jitter, honoring Retry-After.
func (*ddg) Search(ctx context.Context, q string, opts engine.Options) ([]engine.Result, error) {
	reqURL, err := buildRequestURL(q)
	if err != nil {
		return nil, fmt.Errorf("ddg: build request: %w", err)
	}

	// newReq builds a fresh request per retry attempt (headers are constant).
	newReq := func() (*http.Request, error) {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		if err != nil {
			return nil, err
		}
		// DDG's HTML page expects a desktop User-Agent to avoid the mobile redirect.
		httpReq.Header.Set("User-Agent", userAgent)
		httpReq.Header.Set("Accept", "text/html")
		return httpReq, nil
	}

	resp, err := retry.Do(ctx, "ddg", newReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("ddg: read body: %w", err)
	}
	return parseResults(string(body))
}

// buildRequestURL constructs the DuckDuckGo HTML search URL.
func buildRequestURL(q string) (string, error) {
	base, err := url.Parse(scrapeBase)
	if err != nil {
		return "", err
	}
	params := base.Query()
	params.Set("q", q)
	base.RawQuery = params.Encode()
	return base.String(), nil
}

// parseResults parses a DuckDuckGo HTML results page into []engine.Result. It
// is a pure function (no I/O) — ideal for golden-table tests (§14). Selectors
// per the DDG HTML-lite template:
//   - container:  div.result
//   - url:        div.result > a.result__a (href)
//   - title:      div.result h2.result__title
//   - snippet:    div.result .result__snippet
//
// These selectors match the standard DDG HTML results page structure. The
// actual class names may vary slightly between DDG's A/B test variants, but
// this covers the stable "lite" HTML endpoint used by `html.duckduckgo.com`.
func parseResults(htmlStr string) ([]engine.Result, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlStr))
	if err != nil {
		return nil, fmt.Errorf("ddg: parse: %w", err)
	}
	var results []engine.Result
	doc.Find("div.result").Each(func(_ int, div *goquery.Selection) {
		// URL from the result link.
		href, ok := div.Find("a.result__a").First().Attr("href")
		if !ok || href == "" {
			return
		}
		title := strings.TrimSpace(div.Find("h2.result__title").First().Text())
		snippet := strings.TrimSpace(div.Find(".result__snippet").First().Text())
		results = append(results, engine.Result{
			Title:   title,
			URL:     href,
			Snippet: snippet,
		})
	})
	return results, nil
}
