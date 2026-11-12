// Package jina implements the Jina AI Search API engine (§5 Tier D).
//
// Jina is a keyed, LLM-oriented search provider. `s.jina.ai` returns structured
// JSON results from Jina's own index (engine "reader") or from Google/Bing.
// `respondWith=no-content` keeps responses small: we use the description field
// as the snippet instead of full page content.
//
// The engine requires JINA_API_KEY; without it the registry reports
// missing-key and the engine auto-skips.
//
// The HTTP retry/backoff policy (§11) is shared via internal/engine/retry.
package jina

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/openmtx/meta-search-tool/internal/engine"
	"github.com/openmtx/meta-search-tool/internal/engine/retry"
)

// apiBase is the Jina Search endpoint. Tests override it via SetAPIBase.
var apiBase = "https://s.jina.ai/"

// maxFetch bounds one Search call (Jina pages are ~10 results).
const maxFetch = 50

// maxPages bounds pagination independently of maxFetch.
const maxPages = 5

// defaultEngine is Jina's own index.
const defaultEngine = "reader"

// SetAPIBase overrides the API base URL for testing.
func SetAPIBase(base string) { apiBase = base }

// APIBase returns the current API base URL.
func APIBase() string { return apiBase }

// jina is the Jina Search engine implementation.
type jina struct{}

// New returns a new Jina Search engine.
func New() engine.Engine { return &jina{} }

// Name implements engine.Engine.
func (*jina) Name() string { return "jina" }

// Descriptor implements engine.Engine.
func (*jina) Descriptor() engine.EngineInfo {
	return engine.EngineInfo{
		Name:          "jina",
		Keyed:         true,
		KeyEnv:        "JINA_API_KEY",
		DefaultWeight: 1.1,
		Enabled:       false, // opt-in (Tier D, keyed AI search)
		Category:      "general",
	}
}

// Search implements engine.Engine.
func (j *jina) Search(ctx context.Context, q string, opts engine.Options) ([]engine.Result, error) {
	results, _, err := j.fetch(ctx, q, opts)
	return results, err
}

// SearchWithRaw implements engine.RawEngine (§10 --raw).
func (j *jina) SearchWithRaw(ctx context.Context, q string, opts engine.Options) ([]engine.Result, any, error) {
	return j.fetch(ctx, q, opts)
}

// engineMode validates/returns the configured Jina backend.
func engineMode(opts engine.Options) string {
	switch strings.ToLower(strings.TrimSpace(opts.JinaEngine)) {
	case "google":
		return "google"
	case "bing":
		return "bing"
	case "", defaultEngine:
		return defaultEngine
	default:
		return defaultEngine
	}
}

// fetch is the shared request+parse core for Search and SearchWithRaw.
func (j *jina) fetch(ctx context.Context, q string, opts engine.Options) ([]engine.Result, any, error) {
	apiKey := os.Getenv("JINA_API_KEY")
	if apiKey == "" {
		return nil, nil, fmt.Errorf("jina: JINA_API_KEY not set")
	}

	limit := opts.Limit
	if limit < 1 {
		limit = 10
	}
	if limit > maxFetch {
		limit = maxFetch
	}

	mode := engineMode(opts)
	var all []engine.Result
	var raws []any

	for page := 1; page <= maxPages && len(all) < limit; page++ {
		reqURL, err := buildRequestURL(q, page, mode)
		if err != nil {
			return all, rawOrNil(raws), fmt.Errorf("jina: build request: %w", err)
		}

		newReq := func() (*http.Request, error) {
			httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
			if err != nil {
				return nil, err
			}
			httpReq.Header.Set("Accept", "application/json")
			httpReq.Header.Set("Authorization", "Bearer "+apiKey)
			httpReq.Header.Set("User-Agent", engine.UserAgent())
			return httpReq, nil
		}

		resp, err := retry.Do(ctx, "jina", newReq)
		if err != nil {
			return all, rawOrNil(raws), err
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return all, rawOrNil(raws), fmt.Errorf("jina: read body: %w", err)
		}

		var apiResp apiResponse
		if err := json.Unmarshal(body, &apiResp); err != nil {
			return all, rawOrNil(raws), fmt.Errorf("jina: decode response: %w", err)
		}

		var raw any
		_ = json.Unmarshal(body, &raw)
		raws = append(raws, raw)

		pageResults := parseResults(apiResp.Data)
		all = append(all, pageResults...)
		if len(pageResults) == 0 {
			break
		}
	}

	if len(all) > limit {
		all = all[:limit]
	}
	return all, rawOrNil(raws), nil
}

// rawOrNil returns nil when no page was decoded.
func rawOrNil(raws []any) any {
	if len(raws) == 0 {
		return nil
	}
	return raws
}

// buildRequestURL constructs the Jina Search URL.
func buildRequestURL(q string, page int, mode string) (string, error) {
	base, err := url.Parse(apiBase)
	if err != nil {
		return "", err
	}
	params := base.Query()
	params.Set("q", q)
	params.Set("page", strconv.Itoa(page))
	params.Set("engine", mode)
	params.Set("respondWith", "no-content")
	base.RawQuery = params.Encode()
	return base.String(), nil
}

// --- Jina API response types (only the fields we need) ---------------------

type apiResponse struct {
	Data []apiResult `json:"data"`
}

type apiResult struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Description string `json:"description"`
	Date        string `json:"date"`
}

// parseResults maps Jina results into engine.Result slices. It is a pure
// function — no I/O, no side effects — ideal for golden-table tests.
func parseResults(items []apiResult) []engine.Result {
	results := make([]engine.Result, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.URL) == "" {
			continue
		}
		title := strings.TrimSpace(item.Title)
		if title == "" {
			title = item.URL
		}
		results = append(results, engine.Result{
			Title:   title,
			URL:     item.URL,
			Snippet: strings.TrimSpace(item.Description),
		})
	}
	return results
}
