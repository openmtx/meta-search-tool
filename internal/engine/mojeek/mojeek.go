// Package mojeek implements the Mojeek search engine (§5 Tier A).
//
// Mojeek is a keyless HTML scrape engine. It sends a GET request to
// www.mojeek.com/search and parses the HTML response using goquery. It requires
// no API key — StatusReady at all times (unless the site is unreachable, which
// the dispatcher handles via timeout/error).
//
// The HTTP retry/backoff policy (§11) is shared via internal/engine/retry.
package mojeek

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

// scrapeBase is the Mojeek search URL. Tests override it via SetScrapeBase.
var scrapeBase = "https://www.mojeek.com/search"

// userAgent is the HTTP User-Agent sent to Mojeek (§12).
const userAgent = "mst/dev"

// maxPages caps pagination (§5: "max 10 pages, cap fetch at ~100").
const maxPages = 10

// resultsPerPage is Mojeek's default page size; fetching fewer than this on a
// page signals the last page (stop paginating).
const resultsPerPage = 10

// ScrapeBase returns the current scrape base URL (for test restore).
func ScrapeBase() string { return scrapeBase }

// SetScrapeBase overrides the scrape base URL for testing. Callers must restore
// the original value after the test (typically via defer).
func SetScrapeBase(base string) { scrapeBase = base }

// mojeek is the Mojeek search engine implementation.
type mojeek struct{}

// New returns a new Mojeek engine. There is exactly one instance — mojeek is
// stateless (no per-query state, no connection pooling beyond stdlib).
func New() engine.Engine { return &mojeek{} }

// Name implements engine.Engine.
func (*mojeek) Name() string { return "mojeek" }

// Descriptor implements engine.Engine.
func (*mojeek) Descriptor() engine.EngineInfo {
	return engine.EngineInfo{
		Name:          "mojeek",
		Keyed:         false,
		KeyEnv:        "",
		DefaultWeight: 1.3,
		Enabled:       true,
		Category:      "general",
	}
}

// Search implements engine.Engine. It fetches up to maxPages of results from
// Mojeek and parses each HTML page into []engine.Result.
//
// Pagination (§5): page 1 omits the `s` param (sending s=0 triggers a
// rate-limit); subsequent pages use s=10×page. Fetching stops when a page
// returns fewer than resultsPerPage hits (last page) or the fetch limit is met.
//
// Per §7, a paginating engine that fails mid-way returns its accumulated
// results plus the error (partial results with error) rather than discarding
// the pages already fetched.
//
// Retry policy (§11): each page request runs through internal/engine/retry —
// up to 2 retries on HTTP 429/5xx and network errors, exponential backoff,
// ±20% jitter, honoring Retry-After.
func (m *mojeek) Search(ctx context.Context, q string, opts engine.Options) ([]engine.Result, error) {
	results, _, err := m.fetch(ctx, q, opts)
	return results, err
}

// SearchWithRaw implements engine.RawEngine (§10 --raw). For an HTML scrape
// engine the native payload is the raw response body; since Mojeek paginates,
// the payload is the slice of HTML bodies fetched (one per page) — partial on a
// mid-pagination failure, mirroring the partial-results contract (§7).
func (m *mojeek) SearchWithRaw(ctx context.Context, q string, opts engine.Options) ([]engine.Result, any, error) {
	return m.fetch(ctx, q, opts)
}

// fetch is the shared Mojeek pagination core for Search and SearchWithRaw. It
// returns the accumulated results plus the per-page HTML bodies (raw) for --raw.
func (m *mojeek) fetch(ctx context.Context, q string, opts engine.Options) ([]engine.Result, any, error) {
	var allResults []engine.Result
	var pages []string

	for page := 0; page < maxPages; page++ {
		reqURL, err := buildRequestURL(q, page)
		if err != nil {
			return allResults, nonEmpty(pages), fmt.Errorf("mojeek: build request: %w", err)
		}

		// newReq builds a fresh request per retry attempt (headers are constant).
		newReq := func() (*http.Request, error) {
			httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
			if err != nil {
				return nil, err
			}
			httpReq.Header.Set("User-Agent", userAgent)
			httpReq.Header.Set("Accept-Language", "en-US,en;q=0.9")
			return httpReq, nil
		}

		// retry.Do handles the §11 retry loop and returns a 2xx response or an
		// error. On error we return the partial set already collected (§7).
		resp, err := retry.Do(ctx, "mojeek", newReq)
		if err != nil {
			return allResults, nonEmpty(pages), err
		}

		// Read + close the body immediately (no defer-in-loop → no leak across
		// pages). goquery needs the full body in memory anyway.
		body, perr := readAndClose(resp)
		if perr != nil {
			return allResults, nonEmpty(pages), perr
		}
		pages = append(pages, body)

		results, perr := parseResults(body)
		if perr != nil {
			return allResults, nonEmpty(pages), perr
		}
		allResults = append(allResults, results...)

		// Fewer than a full page → last page; stop paginating.
		if len(results) < resultsPerPage {
			return allResults, nonEmpty(pages), nil
		}
		// Reached the fetch limit → no need to fetch more pages.
		if len(allResults) >= opts.Limit {
			return allResults, nonEmpty(pages), nil
		}
	}

	return allResults, nonEmpty(pages), nil
}

// readAndClose reads the HTML body and closes it promptly (no defer-in-loop →
// no leak across pagination pages).
func readAndClose(resp *http.Response) (string, error) {
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("mojeek: read body: %w", err)
	}
	return string(body), nil
}

// nonEmpty returns s, or nil if empty, so an unused --raw entry is omitted
// rather than serialized as `null`.
func nonEmpty(s []string) any {
	if len(s) == 0 {
		return nil
	}
	return s
}

// buildRequestURL constructs the Mojeek search URL.
// Page 0 = first page (omit s param); subsequent pages use s=10*page.
func buildRequestURL(q string, page int) (string, error) {
	base, err := url.Parse(scrapeBase)
	if err != nil {
		return "", err
	}
	params := base.Query()
	params.Set("q", q)
	params.Set("safe", "0")
	// Paging: omit s on page 1 (page=0), otherwise s=10*page.
	// Sending s=0 triggers a rate-limit per spec §5.
	if page > 0 {
		params.Set("s", fmt.Sprintf("%d", 10*page))
	}
	base.RawQuery = params.Encode()
	return base.String(), nil
}

// parseResults parses a Mojeek HTML results page into []engine.Result. It is a
// pure function (no I/O) — ideal for golden-table tests (§14). Selectors per
// spec §5 (adapted from SearXNG to goquery):
//   - container: ul.results-standard > li
//   - url:       li > a.ob/@href
//   - title:     li > h2 text
//   - snippet:   li > p.s text
//
// The spec notes a DOM quirk: <a class="ob"> closes before <h2> (HTML nesting),
// so selectors traverse from li, not from a.ob.
func parseResults(htmlStr string) ([]engine.Result, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlStr))
	if err != nil {
		return nil, fmt.Errorf("mojeek: parse: %w", err)
	}
	var results []engine.Result
	doc.Find("ul.results-standard > li").Each(func(_ int, li *goquery.Selection) {
		// URL from the a.ob link.
		href, ok := li.Find("a.ob").First().Attr("href")
		if !ok || href == "" {
			return
		}
		title := strings.TrimSpace(li.Find("h2").First().Text())
		snippet := strings.TrimSpace(li.Find("p.s").First().Text())
		results = append(results, engine.Result{
			Title:   title,
			URL:     href,
			Snippet: snippet,
		})
	})
	return results, nil
}
