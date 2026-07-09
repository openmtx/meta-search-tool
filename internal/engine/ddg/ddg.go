// Package ddg implements the DuckDuckGo HTML search engine (§5 Tier C).
//
// DuckDuckGo is a keyless HTML scrape engine. It submits a no-JS form POST to
// html.duckduckgo.com/html/ — the same mechanism SearXNG uses (see
// docs/DDG-research.md) — and parses the returned HTML. It requires no API key.
// DDG is **off by default** (§17.3) because datacenter IPs get heavily
// anti-scraped; opt in with `--engine ddg` or `ddg.enabled: true`.
//
// # Bot detection
//
// DDG's html endpoint filters non-browser clients (a non-browser User-Agent or
// missing Sec-Fetch headers returns an anomaly / CAPTCHA page with no results).
// Accordingly ddg follows the SearXNG mechanism: it sends a static desktop
// browser User-Agent plus the Sec-Fetch-*/Referer headers a real no-JS browser
// sends on a form submit, and detects DDG's embedded CAPTCHA challenge
// (`<form id="challenge-form">`) — surfacing it as ErrCaptcha rather than
// silently returning zero results. This browser-UA simulation is an explicit
// carve-out to the honest-UA policy (§12) for scrape engines; structured-API
// engines keep the identifying `mst/<version>` UA.
//
// # Single page
//
// Only page 1 is fetched. DDG needs a `vqd` token (extracted from the page-1
// response, cached per query+UA) to request page 2+; multi-page support is a
// documented follow-up (DECISIONS.md). A single page yields ~10–15 results,
// which the ranker handles gracefully as a smaller candidate pool.
//
// The HTTP retry/backoff policy (§11) is shared via internal/engine/retry.
package ddg

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/openmtx/meta-search-tool/internal/engine"
	"github.com/openmtx/meta-search-tool/internal/engine/retry"
)

// scrapeBase is the DuckDuckGo no-JS HTML search endpoint. Tests override it
// via SetScrapeBase.
var scrapeBase = "https://html.duckduckgo.com/html/"

// ddgUA is the static desktop browser User-Agent sent to DuckDuckGo. It is
// static (not derived from the mst version) because DDG derives its `vqd` token
// from query+UA and bot-blocks non-browser UAs; a realistic, stable browser UA
// is required to pass the html endpoint's filter (§12 carve-out; see package
// doc and docs/DDG-research.md).
const ddgUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36"

// ddgReferer is the Referer sent with every request — the DDG html page a real
// browser would have come from. It is a constant (not derived from scrapeBase)
// because Sec-Fetch-Site is "same-origin": the Referer origin must match the
// request origin (html.duckduckgo.com), which holds for the real endpoint and
// must not change when tests repoint scrapeBase at a local httptest server.
const ddgReferer = "https://html.duckduckgo.com/html/"

// maxQueryLen is the DDG query-length limit: DDG rejects queries with 499+
// characters. Such queries are skipped (no results, no error) rather than sent.
const maxQueryLen = 500

// ErrCaptcha is returned when DDG's response is its embedded bot-detection
// challenge form instead of results. DDG returns this as HTTP 200 with the
// challenge embedded (not a redirect/error), so it is detected after a
// successful fetch. It is not retried — retrying immediately does not clear the
// challenge and may worsen IP reputation (docs/DDG-research.md).
var ErrCaptcha = errors.New("ddg: CAPTCHA challenge (bot detection triggered)")

// SetScrapeBase overrides the scrape base URL for testing. Callers must restore
// the original value after the test (typically via defer).
func SetScrapeBase(base string) { scrapeBase = base }

// ScrapeBase returns the current scrape base URL.
func ScrapeBase() string { return scrapeBase }

// ddg is the DuckDuckGo search engine implementation.
type ddg struct{}

// New returns a new DuckDuckGo engine. There is exactly one instance — ddg is
// stateless (page-1 only; no per-query state, no connection pooling beyond stdlib).
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

// Search implements engine.Engine. It POSTs the no-JS search form to DDG's html
// endpoint and parses the results into []engine.Result. On any error (HTTP
// status >= 400, parse failure, CAPTCHA, context cancellation) it returns nil
// results and a non-nil error. The dispatcher wraps this into EngineRun with
// timing metadata.
//
// Retry policy (§11): delegated to internal/engine/retry — up to 2 retries on
// HTTP 429/5xx and network errors, exponential backoff (base 500ms, factor 2),
// ±20% jitter, honoring Retry-After. A CAPTCHA (HTTP 200 + challenge form) is
// detected post-fetch and returned immediately without retry.
func (d *ddg) Search(ctx context.Context, q string, opts engine.Options) ([]engine.Result, error) {
	results, _, err := d.fetch(ctx, q, opts)
	return results, err
}

// SearchWithRaw implements engine.RawEngine (§10 --raw). For an HTML scrape
// engine the native payload is the raw response body (the HTML has no JSON
// field structure to preserve), so --raw shows exactly what DuckDuckGo returned
// — useful for diagnosing selector/markup drift and CAPTCHA pages.
func (d *ddg) SearchWithRaw(ctx context.Context, q string, opts engine.Options) ([]engine.Result, any, error) {
	return d.fetch(ctx, q, opts)
}

// fetch is the shared DuckDuckGo request+parse core for Search and SearchWithRaw.
func (d *ddg) fetch(ctx context.Context, q string, _ engine.Options) ([]engine.Result, any, error) {
	// DDG rejects queries with 499+ characters. Skip rather than send (matches
	// SearXNG, which sets url=None): no results, no error.
	if len(q) >= maxQueryLen {
		return nil, nil, nil
	}

	formBody := buildRequestBody(q)

	// newReq builds a fresh POST request per retry attempt. The body is a fresh
	// strings.Reader each call so it is re-readable across retries. DDG's html
	// endpoint requires a realistic desktop User-Agent and the Sec-Fetch/Referer
	// headers a real no-JS browser sends on a form submit — otherwise it returns
	// an anomaly/CAPTCHA page (docs/DDG-research.md).
	newReq := func() (*http.Request, error) {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, scrapeBase, strings.NewReader(formBody))
		if err != nil {
			return nil, err
		}
		httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		httpReq.Header.Set("User-Agent", ddgUA)
		httpReq.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
		httpReq.Header.Set("Accept-Language", "en-US,en;q=0.9")
		httpReq.Header.Set("Referer", ddgReferer)
		httpReq.Header.Set("Sec-Fetch-Dest", "document")
		httpReq.Header.Set("Sec-Fetch-Mode", "navigate")
		httpReq.Header.Set("Sec-Fetch-Site", "same-origin")
		httpReq.Header.Set("Sec-Fetch-User", "?1")
		return httpReq, nil
	}

	resp, err := retry.Do(ctx, "ddg", newReq)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("ddg: read body: %w", err)
	}
	results, perr := parseResults(string(body))
	if perr != nil {
		return nil, nil, perr
	}
	return results, string(body), nil
}

// buildRequestBody encodes the DDG no-JS search form (page 1): `q=<query>` plus
// `b=` (empty "beginning" param for first page) and `kl=wt-wt` (all regions).
// Page 2+ would additionally carry `vqd`, `s`, `dc`, `nextParams`, `api=d.js`,
// `o=json`, `v=l` — not used in the single-page implementation.
func buildRequestBody(q string) string {
	form := url.Values{}
	form.Set("q", q)
	form.Set("b", "")
	form.Set("kl", "wt-wt")
	return form.Encode()
}

// parseResults parses a DuckDuckGo HTML results page into []engine.Result. It
// is a pure function (no I/O) — ideal for golden-table tests (§14). If the page
// is DDG's bot-detection challenge form it returns ErrCaptcha. Selectors match
// SearXNG's reverse-engineered `duckduckgo.py` (docs/DDG-research.md):
//   - container: #links > div whose class contains "web-result"
//   - url/title: h2 > a (the result link carries both the href and the title)
//   - snippet:   a.result__snippet
//
// DDG wraps real URLs in a redirect link (//duckduckgo.com/l/?uddg=...) in some
// templates and returns them directly in others, so resolveDDGURL passes direct
// hrefs through and unwraps the `uddg=` form when present.
func parseResults(htmlStr string) ([]engine.Result, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlStr))
	if err != nil {
		return nil, fmt.Errorf("ddg: parse: %w", err)
	}
	if isCaptcha(doc) {
		return nil, ErrCaptcha
	}
	return parseDoc(doc), nil
}

// isCaptcha reports whether the document is DDG's bot-detection challenge form.
// DDG returns it as HTTP 200 with `<form id="challenge-form">` embedded.
func isCaptcha(doc *goquery.Document) bool {
	return doc.Find("form#challenge-form").Length() > 0
}

// parseDoc extracts results from a parsed DDG page (no CAPTCHA check).
func parseDoc(doc *goquery.Document) []engine.Result {
	var results []engine.Result
	doc.Find("#links div[class*=web-result]").Each(func(_ int, div *goquery.Selection) {
		link := div.Find("h2 a").First()
		href, ok := link.Attr("href")
		if !ok || href == "" {
			return
		}
		u := resolveDDGURL(href)
		if u == "" {
			return
		}
		results = append(results, engine.Result{
			Title:   strings.TrimSpace(link.Text()),
			URL:     u,
			Snippet: strings.TrimSpace(div.Find("a.result__snippet").First().Text()),
		})
	})
	return results
}

// resolveDDGURL extracts the real destination from a DDG result href. The html
// endpoint wraps destinations in a redirect (`//duckduckgo.com/l/?uddg=<enc>`)
// in some templates and returns them directly in others. Direct hrefs pass
// through unchanged; the `uddg=` form is unwrapped to the real destination.
func resolveDDGURL(href string) string {
	if !strings.Contains(href, "uddg=") {
		return href
	}
	u, err := url.Parse(href)
	if err != nil {
		return href
	}
	dst := u.Query().Get("uddg") // already URL-decoded
	if dst == "" {
		return href
	}
	if strings.HasPrefix(dst, "//") {
		dst = "https:" + dst
	}
	return dst
}
