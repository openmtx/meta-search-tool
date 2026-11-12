// Package europepmc implements the Europe PMC REST API engine (§5 Tier B).
//
// Europe PMC is a keyless structured-API engine covering life-sciences
// literature, preprints, and open-access full text. It complements PubMed: the
// REST API returns rich metadata (journal, DOI, authors, OA links) in JSON, and
// cursor pagination lets one Search call fill the fetch limit.
//
// The HTTP retry/backoff policy (§11) is shared via internal/engine/retry.
package europepmc

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/openmtx/meta-search-tool/internal/engine"
	"github.com/openmtx/meta-search-tool/internal/engine/retry"
)

// apiBase is the Europe PMC REST search endpoint. Tests override it via
// SetAPIBase.
var apiBase = "https://www.ebi.ac.uk/europepmc/webservices/rest/search"

// articleBase is the public article URL prefix.
const articleBase = "https://europepmc.org/article/"

// maxFetch is the maximum number of normalized results one Search call will
// collect. It bounds cursor pagination for very large --limit values.
const maxFetch = 100

// pageSize is the number of records requested per REST call. The API supports
// larger pages, but 25 keeps latency and response size reasonable while cursor
// pagination fills larger limits.
const pageSize = 25

// maxPages bounds cursor pagination independently of maxFetch.
const maxPages = 10

// SetAPIBase overrides the API base URL for testing. Callers must restore the
// original value after the test (typically via defer).
func SetAPIBase(base string) { apiBase = base }

// APIBase returns the current API base URL.
func APIBase() string { return apiBase }

// europepmc is the Europe PMC search engine implementation.
type europepmc struct{}

// New returns a new Europe PMC engine.
func New() engine.Engine { return &europepmc{} }

// Name implements engine.Engine.
func (*europepmc) Name() string { return "europepmc" }

// Descriptor implements engine.Engine.
func (*europepmc) Descriptor() engine.EngineInfo {
	return engine.EngineInfo{
		Name:          "europepmc",
		Keyed:         false,
		KeyEnv:        "",
		DefaultWeight: 1.1,
		Enabled:       true,
		Category:      "academic",
	}
}

// Search implements engine.Engine.
func (e *europepmc) Search(ctx context.Context, q string, opts engine.Options) ([]engine.Result, error) {
	results, _, err := e.fetch(ctx, q, opts)
	return results, err
}

// SearchWithRaw implements engine.RawEngine (§10 --raw): it returns the decoded
// native JSON responses (one per cursor page) alongside the normalized results.
func (e *europepmc) SearchWithRaw(ctx context.Context, q string, opts engine.Options) ([]engine.Result, any, error) {
	return e.fetch(ctx, q, opts)
}

// fetch is the shared request+parse core for Search and SearchWithRaw.
func (e *europepmc) fetch(ctx context.Context, q string, opts engine.Options) ([]engine.Result, any, error) {
	limit := opts.Limit
	if limit < 1 {
		limit = 1
	}
	if limit > maxFetch {
		limit = maxFetch
	}

	cursor := "*"
	var all []engine.Result
	var raws []any

	for page := 0; page < maxPages && len(all) < limit; page++ {
		reqURL, err := buildRequestURL(q, cursor)
		if err != nil {
			return all, rawOrNil(raws), fmt.Errorf("europepmc: build request: %w", err)
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

		resp, err := retry.Do(ctx, "europepmc", newReq)
		if err != nil {
			return all, rawOrNil(raws), err
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return all, rawOrNil(raws), fmt.Errorf("europepmc: read body: %w", err)
		}

		var apiResp apiResponse
		if err := json.Unmarshal(body, &apiResp); err != nil {
			return all, rawOrNil(raws), fmt.Errorf("europepmc: decode response: %w", err)
		}

		var raw any
		_ = json.Unmarshal(body, &raw)
		raws = append(raws, raw)

		all = append(all, parseResults(apiResp.ResultList.Result)...)
		if len(all) >= limit {
			break
		}
		if apiResp.NextCursorMark == "" || apiResp.NextCursorMark == cursor {
			break
		}
		cursor = apiResp.NextCursorMark
	}

	if len(all) > limit {
		all = all[:limit]
	}
	return all, rawOrNil(raws), nil
}

// rawOrNil returns the raw pages, or nil when no page was decoded, so --raw
// omits the engine rather than serializing an empty array.
func rawOrNil(raws []any) any {
	if len(raws) == 0 {
		return nil
	}
	return raws
}

// buildRequestURL constructs the Europe PMC search URL. The cursorMark starts
// at "*" and is replaced by nextCursorMark for subsequent pages.
func buildRequestURL(q, cursor string) (string, error) {
	base, err := url.Parse(apiBase)
	if err != nil {
		return "", err
	}
	params := base.Query()
	params.Set("query", q)
	params.Set("format", "json")
	params.Set("resultType", "core")
	params.Set("pageSize", fmt.Sprintf("%d", pageSize))
	params.Set("cursorMark", cursor)
	base.RawQuery = params.Encode()
	return base.String(), nil
}

// --- Europe PMC REST response types (only the fields we need) ---------------

// apiResponse is the top-level search response.
type apiResponse struct {
	NextCursorMark string `json:"nextCursorMark"`
	ResultList     struct {
		Result []apiResult `json:"result"`
	} `json:"resultList"`
}

// apiResult is one record in resultList.result.
type apiResult struct {
	ID                   string `json:"id"`
	Source               string `json:"source"`
	Title                string `json:"title"`
	AbstractText         string `json:"abstractText"`
	AuthorString         string `json:"authorString"`
	JournalTitle         string `json:"journalTitle"`
	PubYear              string `json:"pubYear"`
	FirstPublicationDate string `json:"firstPublicationDate"`
}

// htmlTagRe strips HTML tags from titles/abstracts.
var htmlTagRe = regexp.MustCompile(`<[^>]*>`)

// cleanText strips HTML tags, unescapes entities, and collapses whitespace.
func cleanText(s string) string {
	s = htmlTagRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	return strings.Join(strings.Fields(s), " ")
}

// parseResults maps Europe PMC records into engine.Result slices. It is a pure
// function — no I/O, no side effects — ideal for golden-table tests. Records
// without an id/title are skipped; the snippet prefers the abstract and falls
// back to journal/year metadata.
func parseResults(items []apiResult) []engine.Result {
	results := make([]engine.Result, 0, len(items))
	for _, item := range items {
		id := strings.TrimSpace(item.ID)
		title := cleanText(item.Title)
		source := strings.TrimSpace(item.Source)
		if id == "" || title == "" || source == "" {
			continue
		}

		snippet := cleanText(item.AbstractText)
		if snippet == "" {
			var parts []string
			if item.JournalTitle != "" {
				parts = append(parts, cleanText(item.JournalTitle))
			}
			if item.PubYear != "" {
				parts = append(parts, item.PubYear)
			}
			snippet = strings.Join(parts, ". ")
		}

		results = append(results, engine.Result{
			Title:   title,
			URL:     articleBase + source + "/" + id,
			Snippet: snippet,
		})
	}
	return results
}
