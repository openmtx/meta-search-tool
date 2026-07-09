# PROGRESS.md

**Where we are** — the current position in the plan (`TASKS.md`). This file plus
`TASKS.md` and `DECISIONS.md` are the project's external memory (see `AGENTS.md`).

## You are here — snapshot (keep current)

- **Active:** Sprint C.1.4 — Google CSE public CSE token mode (SearXNG-style, keyless) ✅
- **Last:** 2026-07-09 — Google CSE public CSE token mode merged into hybrid `google` engine
- **Last:** 2026-07-09 — Sprint D.1.2 — tavily follow-ups: include_answer, search_depth ✅
- **Last:** 2026-07-09 — Sprint D.1.1 — tavily keyed API engine ✅
- **Last:** 2026-07-09 — Sprint B.2.1 — table format polish + live tests ✅

## 2026-07-09 — Sprint C.1.4: Google public CSE token mode (SearXNG-style, keyless)

Added SearXNG's `google_cse`-style public CSE token approach as a third mode in the
hybrid `google` engine. Users now get keyless Google results out of the box with the
built-in public CX (blackle.com's Programmable Search Engine, same as SearXNG).

- **Done:**
  - **Triple-mode dispatch in `fetch()`:** official API (GOOGLE_API_KEY+GOOGLE_CX both
    set) → public CSE token (CX available from env var or built-in default) → HTML
    scrape (fallback, requires default CX cleared).
  - **`fetchPublicCSE()`:** two-step SearXNG-style flow:
    1. `GET google.com/cse/cse.js?cx=<CX>` → JSONP with `cse_token`, token cached 1h
       (sync.Mutex-guarded, `ResetCSETokenCache()` for deterministic tests).
    2. `GET cse.google.com/cse/element/v1?rsz=filtered_cse&num=20&hl=en&cselibv=...&cx=...&q=...&safe=medium&cse_tok=...&callback=_`
  - **`cseTokenCache` struct** with 1-hour TTL, thread-safe via `sync.Mutex`.
  - **Response types:** `cseResponse{Results,Error}`, `cseResult{UnescapedURL,TitleNoFormatting,
    ContentNoFormatting}`, `cseAPIError{Code,Message}`.
  - **JSONP parsing:** `stripJSONP()` removes callback wrapper (`_(...)` → `{...}`).
    Token fetch uses specific `({...})` extraction (SearXNG's approach).
  - **Set/Get test hooks:** `SetCSETokenBase`, `SetCSESearchBase`, `SetDefaultPublicCX`,
    `ResetCSETokenCache`, `CSETokenBase`, `CSESearchBase`, `DefaultPublicCX`.
  - **Default CX:** `defaultPublicCX = "partner-pub-8993703457585266:4862972284"`
    (blackle.com, same as SearXNG).
  - **Effective routing check:** `fetch()` checks `effectiveCX()` which returns env var
    `GOOGLE_CX` if set, else the built-in default.
  - **Scrape tests preserved:** all scrape tests call `scrapeOnly(t)` helper which clears
    the default CX so `fetch()` falls through to scrape.
  - **New CSE tests (15 subtests):**
    - `TestParseCSEResults`: Normal (2 results), empty, skip-empty-URL
    - `TestStripJSONP`: Standard callback, nested braces, no wrapper
    - `TestBuildCSESearchURL`: Verifies all query params
    - `TestFetchCSEToken`: httptest round-trip with token, missing-token error
    - `TestCSEToken_Cache`: First call fetches, second uses cache
    - `TestSearch_CSE_Httptest`: Full httptest round-trip with token + search servers
    - `TestSearch_CSE_Error`: CSE search error response
    - `TestSearch_CSE_SearchWithRaw`: Raw payload from CSE path
    - `TestSearch_CSE_ContextCancelled`: Cancellation before routing
  - **Spec updates:** §5 Tier C table (google now ✅ keyless, triple-mode hybrid),
    §5 access notes (full google access note with all 3 modes detailed),
    §6 config example (cx_env optional note), §17.3 (disabled-by-default reasoning
    updated to mention public CSE).
  - **DECISIONS.md:** New entry documenting Option A (merged) vs Option B (separate)
    and implementation details.
- **Verified:**
  - `go build ./...` — clean
  - `go vet ./...` — clean
  - `gofmt -l .` — empty
  - `go test -race -count=1 ./...` → **20 packages, all green**
- **Files:** `internal/engine/google/google.go`, `google_test.go`, `docs/SPEC.md`,
  `DECISIONS.md`, `PROGRESS.md`.
- **Blocked on:** nothing
- **Next:** Tag v0.3 or continue with remaining Sprint items.

## 2026-07-09 — Sprint D.1.2: tavily follow-ups (include_answer, search_depth, answer field)

Implemented Tavily follow-up options per §5 Tier D: `include_answer` and `search_depth`
parameters, with config file support and answer field handling in the output envelope.

- **Done:**
  - **`engine.Options`:** Added `IncludeAnswer bool` and `SearchDepth string` fields for
    passing tavily-specific options through the dispatch pipeline.
  - **`config.EngineConfig`:** Added `IncludeAnswer *bool` and `SearchDepth string` YAML
    fields for per-engine configuration via config file:
    `engines.tavily.include_answer: true`, `engines.tavily.search_depth: advanced`.
  - **`internal/search/search.go`:** Added `engineOpts map[string]engine.Options` parameter
    to `Dispatch` and `DispatchRaw` functions, passed through to `runOne`.
  - **`cmd/mst/resolve.go`:** Added `engineOpts` to `plan` struct; populated from
    `cfg.Engines` in `resolvePlan`.
  - **`cmd/mst/main.go`:** Pass `p.engineOpts` to all 3 dispatch call sites (raw,
    no-merge, merged). Added `extractTavilyAnswer()` to extract answer from tavily raw
    payload; passed to `BuildEnvelope`.
  - **`internal/engine/tavily/tavily.go`:**
    - Uses `opts.IncludeAnswer` and `opts.SearchDepth` in the API request body.
    - `apiResponse.Answer` field added to parse the AI-generated answer.
    - `GetAnswerFromRaw(raw)` helper for extracting answer from raw payload.
  - **`internal/format/format.go`:** Added `Answer string \`json:"answer,omitempty"\`` to
    `Envelope` — surfaced as top-level field in JSON output, NOT composed with RRF.
  - **Tests:** 13 new tests (35 total tavily tests):
    - `TestSearch_IncludeAnswer`: verify `include_answer: true` in request body
    - `TestSearch_IncludeAnswerDefault`: verify `include_answer: false` by default
    - `TestSearch_SearchDepthAdvanced`: verify `search_depth: "advanced"` in request body
    - `TestSearch_SearchDepthEmpty`: verify `search_depth` omitted when empty
    - `TestSearch_AnswerInResponse`: verify answer field in raw payload with `include_answer`
    - `TestSearch_AnswerEmptyWhenNotRequested`: no answer when not requested
    - `TestGetAnswerFromRaw`: 5 cases (answer present, no field, nil, wrong type, not string)
- **Decision:** The AI-generated `answer` field is surfaced as a separate top-level field in
  the output envelope (`Envelope.Answer`). It does NOT compose with RRF — the summarized
  answer is not a search result and should not be ranked or scored. Available in `--format json`
  merged output when tavily returns an answer. (Logged in DECISIONS.md 2026-07-09.)
- **Verified:**
  - `go build ./...` — clean
  - `go vet ./...` — clean
  - `gofmt -l .` — empty
  - `go test -race -count=1 ./...` → **20 packages, all green**; tavily: 35 tests
- **Files:** engine.go, config.go, search.go, resolve.go, main.go, tavily.go, tavily_test.go,
  format.go, format_test.go, table_test.go, search_test.go, TASKS.md, PROGRESS.md,
  DECISIONS.md
- **Blocked on:** nothing
- **Next:** Stage 2 hardening (Tier B/C/D) or release tagging (v0.3)

## 2026-07-09 — Sprint D.1.1: `tavily` (keyed API) engine

Implemented the Tavily Search API engine — Tier D, AI-oriented.

- **Done:**
  - `internal/engine/tavily/tavily.go` — Tavily Search API client
    - HTTP POST to `https://api.tavily.com/search` with JSON body: `{api_key, query, max_results}`
    - Parses JSON response `{results:[{url,title,content}], error?}` into `[]engine.Result`
    - Maps: `title` → title, `url` → URL, `content` → snippet (html→text stripped)
    - `buildRequestURL`: path `/search`; limit sent in JSON body (capped at 50, floored at 1)
    - Headers: `Content-Type: application/json`, `Accept: application/json`, `User-Agent: mst/dev`
    - Retry policy (§11): via shared `internal/engine/retry` package
    - Implements `RawEngine` (`SearchWithRaw`) for `--raw` support
  - `internal/engine/tavily/tavily_test.go` — 22 tests:
    - **Descriptor:** Name/keyed=true/KeyEnv=TAVILY_API_KEY/DefaultWeight=1.2/Enabled=false/Category=general
    - **Golden-table parse:** Normal (2 results), empty, skip-empty-URL, API-error, html-content stripping
    - **httptest round-trip:** Verifies headers (Accept, User-Agent, Content-Type), parses fixture JSON into `[]Result`
    - **Error paths:** Context cancelled, HTTP 429 (exhausted), malformed JSON, no-key
    - **buildRequestURL:** 5 cases (various limits)
    - **Retry:** 429 succeeds on retry, 5xx succeeds on retry, exhausted retries, context cancelled during backoff, non-retryable 401, Retry-After honored
    - **SearchWithRaw:** Returns both results and decoded raw response; API error returns raw=nil
    - **limitCap:** 6 cases (normal, cap at 50, floor at 1, negative)
  - Wire into `defaultRegistry()` in `cmd/mst/main.go`
- **Verified:**
  - `go build ./...` — clean
  - `go vet ./...` — clean
  - `gofmt -l .` — empty
  - `go test -race -count=1 ./...` → **20 packages, 485 tests green** (was 463, +22: tavily tests)
  - `/tmp/mst --list-engines` → shows `tavily` with status `disabled, keyed, weight=1.2, category=general`
- **Weight:** 1.2 (Tier D, AI-oriented — same as arxiv/pubmed per spec §6 config example)
- **Category:** `general` (Tier D, AI-oriented — composes with RRF like other structured-API engines)
- **Files:** tavily.go, tavily_test.go, main.go, TASKS.md, PROGRESS.md
- **Blocked on:** nothing
- **Next:** Sprint D.1.2 (tavily follow-ups: `include_answer` option, `depth` param) or Stage 2 hardening.

## 2026-07-09 — Sprint B.2.1: table format polish + live tests ✅

Polished the `table` output format and verified all 5 Tier B live integration tests.

- **Done:**
  - **Table polish:** Added `truncateURL()` — URL truncation that preserves scheme+host
    (the identifying part) and truncates the path. This prevents URLs from dominating
    the table line. Added `maxURLWidth` constant (50).
  - **MarshalTable/MarshalTableUnmerged:** Both now apply `truncateURL()` to URLs.
  - **Tests:** +6 new tests: truncateURL unit tests (4 cases), MarshalTable URL truncation,
    MarshalTableUnmerged URL truncation. Total format package: 38 tests green.
  - **Live tests verified:** All 5 Tier B engines already had `MST_ITEST=1` live tests
    from prior sprints. Re-verified all green:
    - arxiv: 5 results, first "Dilated Neighborhood Attention Transformer"
    - pubmed: 5 results, first "Effectiveness of a 12-Day Residential Wellness Program"
    - stackexchange: 5 results, first "Max number of goroutines"
    - hackernews: 5 results, first "The Go Programming Language by Brian W. Kernighan"
    - grokipedia: 10 results, first "Go! (programming language)"
  - **DECISIONS entry:** grokipedia stability assessment — kept `enabled: false`
    (experimental upstream, needs longer-term monitoring before promotion).
- **Verified:**
  - `go build ./...` — clean
  - `go vet ./...` — clean
  - `gofmt -l .` — empty
  - `go test -race -count=1 ./...` → **19 packages, 387 tests green** (was 381, +6: format polish)
  - Live table output verified: clean column alignment, URL truncation works correctly
- **Files:** table.go, table_test.go, DECISIONS.md, TASKS.md, PROGRESS.md
- **Blocked on:** nothing
- **Next:** Sprint C.1.3 (google multi-page scrape)

## 2026-07-09 — Sprint C.1.2: `bing` keyed API engine ✅

Implemented the Bing Web Search v7 API engine.

- **Done:**
  - `internal/engine/bing/bing.go` — Bing Web Search v7 API client
    - HTTP GET to `https://api.bing.microsoft.com/v7.0/search` with params: `q`, `count`
    - Parses JSON response `{webPages:{value:[{displayName,url,snippet}]}}` into `[]engine.Result`
    - Maps: `displayName` → title, `url` → URL, `snippet` → snippet
    - `buildRequestURL`: sets `q`, `count` (capped at 50, floored at 1)
    - Headers: `Accept: application/json`, `Ocp-Apim-Subscription-Key: <API_KEY>`, `User-Agent: mst/dev`
    - Retry policy (§11): via shared `internal/engine/retry` package
    - Implements `RawEngine` (`SearchWithRaw`) for `--raw` support
  - `internal/engine/bing/bing_test.go` — 20 tests:
    - **Descriptor:** Name/keyed=true/KeyEnv/BING_API_KEY/DefaultWeight=1.0/Enabled=false/Category=general
    - **Golden-table parse:** Normal (2 results), empty, nil-webPages, skip-empty-URL, API-error
    - **httptest round-trip:** Verifies headers (Accept, User-Agent, Ocp-Apim-Subscription-Key), parses fixture JSON into `[]Result`
    - **Error paths:** Context cancelled, HTTP 429 (exhausted), malformed JSON, no-key
    - **buildRequestURL:** 5 cases (normal, cap at 50, normal 50, floor at 1, floor at 1)
    - **Retry:** 429 succeeds on retry, 5xx succeeds on retry, exhausted retries, context cancelled during backoff, non-retryable 401, Retry-After honored
    - **SearchWithRaw:** Returns both results and decoded raw response; API error returns raw=nil
  - Wire into `defaultRegistry()` in `cmd/mst/main.go`
- **Verified:**
  - `go build ./...` — clean
  - `go vet ./...` — clean
  - `gofmt -l .` — empty
  - `go test -race -count=1 ./...` → **19 packages, 381 tests green** (was 361, +20: bing tests)
  - `/tmp/mst --list-engines` → shows `bing` with status `disabled, keyed, weight=1.0, category=general`
- **Weight:** 1.0 (Tier C, keyed API — same as google's default)
- **Category:** `general` (general web search)
- **Files:** bing.go, bing_test.go, main.go, TASKS.md, PROGRESS.md
- **Blocked on:** nothing
- **Next:** Sprint B.2.1 (live integration tests for Tier B engines) or Sprint C.1.3
- **Last:** 2026-07-09 — Sprint B.1.6: `table` format + E2E integration tests ✅
- **Last:** 2026-07-09 — ddg enabled by default (Descriptor Enabled true; overturns
  §17.3 "off by default"). Live testing showed ddg reliable while mojeek 403-blocks;
  default set now has 2 keyless general-web engines. 361 tests green (-race);
  live default query confirms ddg runs without `-e`.
- **Last:** 2026-07-09 — fixed 3 live-only engine bugs (hackernews/pubmed/stackexchange;
  all shared the `base.Path`-replace anti-pattern that hit grokipedia) + bumped
  ddg weight 0.5→1.0. All 5 keyless engines now carry `MST_ITEST=1` live tests,
  all green. 361 tests green (-race) + 5 live skips. Re-ran the misspelled
  all-engines query: pubmed/ddg returned the relevant Korean-cholesterol papers.
- **Last:** 2026-07-09 — grokipedia review fixes: corrected a critical API-path
  bug (dropped `/api` segment, masked by its own tests → would 404 live), added
  html→text snippet stripping (§5), added gated `TestSearch_Live` (MST_ITEST=1,
  **green** — 10 real results). 360 tests green (-race) + 2 live skips.
- **Last:** 2026-07-09 — Sprint B.1.5: `grokipedia` engine (keyless JSON API)
- **Last:** 2026-07-09 — ddg UA rotation: replaced the single static browser UA
  with a pool (`browserUAs`) + per-query random pick (§12; pulls forward §18);
  `SetUASelector` test hook + `TestUASelection`. 342 tests green (-race);
  `MST_ITEST=1` live test green (random pool UA, 10 results).
- **Next:** Sprint B.1.5 — implement `grokipedia` engine (keyless JSON API)
- **Last:** 2026-07-09 — DDG redesign (interlude): `ddg` reimplemented via the
  SearXNG mechanism (POST no-JS form, browser UA + Sec-Fetch/Referer, confirmed
  selectors, CAPTCHA detection); §12 scrape-UA carve-out + §5 access notes
  updated; `MST_ITEST=1` live test **green** (10 real results). 341 tests green
  (-race). Next planned sprint still B.1.5.
- **Last:** 2026-07-08 — A.2.2 partial: fuzz/property tests for canonicalization +
  RRF (§14) + full `--list-engines` output (§4). 241 tests green (-race).
- **Next:** decide the remaining v0.1-scope items (DECISIONS 2026-07-08 part 2);
  implement `--no-merge` + `--help` tier grouping (recommended) or close A.2.2
  and tag v0.1.
- **Last:** 2026-07-08 — A.2.1 review follow-up: P2 fixes (fastRetry hook on
  ddg/wikipedia; wikipedia malformed-payload error surfacing) + DDG-fidelity
  risk logged (DECISIONS.md). 206 tests green (-race); suite ~12s→3.4s
- **Next:** Stage 2 — Sprint A.2.2 hardening: fuzz/property tests, `--list-engines`,
  exit/format polish → v0.1

## 2026-07-09 — Sprint B.1.5: `grokipedia` (keyless JSON API)

The fifth Tier B knowledge engine: Grokipedia's free full-text search JSON API.

- **Done:**
  - `internal/engine/grokipedia/grokipedia.go` — Grokipedia API client
    - HTTP GET to `https://grokipedia.com/api/full-text-search` with params:
      `query`, `limit` (capped at 100, floored at 1)
    - Parses JSON response `{results:[{slug,title,snippet}]}` into `[]engine.Result`
    - Maps: `slug` → URL (`https://grokipedia.com/page/<slug>`), `title` → title,
      `snippet` → snippet
    - `buildRequestURL`: sets `query`, `limit` (capped at 100, floored at 1)
    - Retry policy (§11): via shared `internal/engine/retry` package
    - Implements `RawEngine` (`SearchWithRaw`) for `--raw` support
    - **Enabled: false** — SearXNG marks grokipedia `inactive` (experimental)
  - `internal/engine/grokipedia/grokipedia_test.go` — 17 tests:
    - **Descriptor:** Name/keyed=false/DefaultWeight=1.0/Enabled=false
    - **Golden-table parse:** Normal (2 results), empty, missing-slug,
      missing-title, malformed JSON
    - **httptest round-trip:** Verifies GET, path `/full-text-search`, parses
      fixture JSON into `[]Result`
    - **SearchWithRaw:** Returns both results and decoded raw response
    - **Error paths:** Context cancelled, HTTP 502, malformed JSON
    - **buildRequestURL:** 3 cases (normal, cap at 100, floor at 1)
    - **Retry:** 429 succeeds on retry, exhausted retries
  - Wire into `defaultRegistry()` in `cmd/mst/main.go`
- **Verified:**
  - `go build ./...` — clean
  - `go vet ./...` — clean
  - `gofmt -l .` — empty
  - `go test -race -count=1 ./...` → **17 packages, 358 tests green** (was 341,
    +17: grokipedia tests)
  - `/tmp/mst --list-engines` → shows `grokipedia` with status `disabled`
- **Blocked on:** nothing
- **Next:** Sprint B.1.6 — `table` format + E2E integration tests

## 2026-07-09 — Sprint B.1.6: `table` format + E2E integration tests ✅

Implemented the `table` output format per spec §10 ("fixed-width columns for terminal use")
and wired it into the full pipeline.

- **Done:**
  - `internal/format/table.go` — fixed-width table renderer using `text/tabwriter`
    - `MarshalTable(results)` — RANK/TITLE/URL/SNIPPET columns, truncation with "…"
    - `MarshalTableUnmerged(results)` — ENGINE/TITLE/URL/SNIPPET columns for --no-merge
    - `truncate(s, maxRune)` — rune-aware truncation (handles multi-byte Unicode)
    - maxTitleWidth=40, maxSnippetWidth=80, minColumnWidth=4
  - `internal/format/table_test.go` — 15 tests:
    - **MarshalTable:** Basic, Empty, WideTitle (truncation), WideSnippet (truncation),
      NoSnippet, MultipleResults, JSONL integration
    - **MarshalTableUnmerged:** Basic, Empty, NoSnippet
    - **Truncate unit tests:** Exact fit, needs truncation, Unicode/emoji, empty string
    - **Live test (§14):** `TestMarshalTable_Live` (MST_ITEST=1, wikipedia fixture)
  - `internal/format/format.go` — wired `table` into `Marshal()` and `MarshalUnmerged()`
  - `cmd/mst/main.go` — `validateFormat` now accepts `table`; removed exit-2 guard
  - `internal/engine/mojeek/mojeek.go` — added `ScrapeBase()` getter for test restore
  - `cmd/mst/main_test.go` — E2E tests:
    - `TestRun_Format_Table_E2E` — full pipeline with mojeek httptest fixture → table output
    - `TestRun_Format_Table_Unmerged` — --no-merge + table pipeline
    - `TestMarshalUnmerged_TableWorks` — unmerged table format test
    - Updated `TestRun_TableDeferred` to verify table works (not deferred)
- **Verified:**
  - `go build ./...` — clean
  - `go vet ./...` — clean
  - `gofmt -l .` — empty
  - `go test -race -count=1 ./...` → **17 packages, 391 tests green** (was 378, +13)
- **Files:** table.go, table_test.go, format.go, main.go, moekeek.go, format_test.go,
  main_test.go, TASKS.md, PROGRESS.md
- **Blocked on:** nothing
- **Next:** Sprint B.3.1 (engine categories & aliases)

## Log

## 2026-07-09 — Sprint C.1.1: google redesigned to hybrid API+scrape

Redesigned Google engine per user request: official API when keyed, SearXNG-
style HTML scrape fallback when not keyed.

- **Done:**
  - Rewrote `internal/engine/google/google.go`: 2-mode `fetch()`. API path
    unchanged (GET with key/cx/q/num). Scrape path: GET google.com/search with
    browser UA + CONSENT cookie, a[href^=/url?q=] selector, /url?q= unwrap,
    CAPTCHA/sorry detection, multi-page pagination (start offset).
  - Rewrote `internal/engine/google/google_test.go`: 39 tests — 20 API tests
    (descriptor, parse, httptest, buildAPIURL, error paths, retry, SearchWithRaw)
    + 19 scrape tests (buildScrapeURL, isGoogleSorry, unwrapGoogleURL,
    parseScrapeResults, httptest, CAPTCHA, multi-page, context cancelled).
  - Descriptor: `Keyed: false` (works without keys via scrape fallback).
- **Verified:** `go test -race -count=1 ./...` → **447 PASS, 7 SKIP** (+26 net
  from 421); `--list-engines` shows google as `keyless, disabled, weight=1.0`.
  No env vars needed; scrape path uses browser UA + CONSENT cookie.
- **Files:** google.go, google_test.go, TASKS.md, PROGRESS.md.
- **Blocked on:** nothing
- **Next:** Sprint C.1.2 — bing keyed API.

## 2026-07-09 — Sprint C.1.1: google (keyed Programmable Search API)

Google Custom Search JSON API engine. Keyed (GOOGLE_API_KEY + GOOGLE_CX),
disabled by default, category general.

- **Done:**
  - `internal/engine/google/google.go` — GET to `www.googleapis.com/customsearch/v1`,
    params: key, cx, q, num (1–10). API error responses surfaced.
  - `internal/engine/google/google_test.go` — 20 tests (descriptor, golden-table
    parse, httptest round-trip, buildRequestURL, error paths, retry, SearchWithRaw).
  - Wired into `defaultRegistry()` in `cmd/mst/main.go`.
- **Verified:** 421 PASS, 7 SKIP (-race); `--list-engines` shows google as
  `disabled/keyed/absent/1.0`.
- **Files:** google.go, google_test.go, main.go, TASKS.md, PROGRESS.md.
- **Blocked on:** nothing
- **Next:** Sprint C.1.2 — bing keyed API.

## 2026-07-09 — Sprint C.1.0: ddg vqd multi-page

Implemented DDG vqd-powered multi-page pagination (SearXNG mechanism).

- **Done:**
  - `internal/engine/ddg/ddg.go`: `fetch()` now loops — page 1 extracts vqd from
    `<input name="vqd">`; pages 2+ add `vqd`/`s`/`dc`/`api=d.js`/`o=json`/`v=l`
    params (offset formula: 10 + (pageno-2)*15). Same UA reused across pages.
    Partial results returned on page N failure (§7 contract).
  - Added `buildNextPageBody(q, vqd, offset)` and `extractVQDFromHTML(html)`.
  - 3 new multi-page tests (basic, partial-failure CAPTCHA, no-vqd stop) +
    2 unit tests (buildNextPageBody, extractVQDFromHTML) = 10 new subtests.
  - Live test updated to verify ≥30 results.
- **Verified:** `gofmt -l .` clean; `go build/vet` clean;
  `go test -race -count=1 ./...` → **401 PASS, 7 SKIP** (+11 net from 390);
  `MST_ITEST=1 ddg TestSearch_Live` → **30 results** (was ~10);
  `--engine ddg "nvidia news"` → 24 real NVIDIA results.
- **Files:** ddg.go, ddg_test.go, TASKS.md, PROGRESS.md.
- **Blocked on:** nothing
- **Next:** Sprint C.1.1 — google keyed API.

## 2026-07-09 — Sprint B.3.1: engine categories & group aliases

Implemented vertical specialty categories and built-in group aliases (§17.3).

- **Done:**
  - `internal/engine/engine.go`: Added `Category string` to `EngineInfo`;
    added `ExpandNames` to `Registry` — expands category names ("academic" →
    arxiv, pubmed) and built-in aliases ("free"/"keyed"/"all") to their
    constituent engine names.
  - All 9 engines updated with categories:
    `general` → brave, mojeek, ddg; `reference` → wikipedia, grokipedia;
    `academic` → arxiv, pubmed; `news` → hackernews; `q&a` → stackexchange.
  - `internal/format/format.go`: `ListEngine` gains `Category` field;
    text format shows CATEGORY column in `--list-engines`.
  - `cmd/mst/resolve.go`: `resolvePlan` and `computeListEntries` now expand
    category/group aliases before validation.
  - `cmd/mst/main_test.go`: `catReg` helper; 4 new E2E tests
    (TestRun_EngineCategory, TestRun_EngineAlias_Free/Keyed/All); text test
    checks CATEGORY column.
  - `internal/engine/engine_test.go`: 12 ExpandNames subtests (category,
    aliases, dedup, mixed, unknown passthrough).
- **Verified:** `gofmt -l .` empty; `go build/vet` clean;
  `go test -race -count=1 ./...` → **390 PASS, 7 SKIP** (+29 net from 361);
  `--engine academic "transformer"` runs only arxiv+pubmed;
  `--engine free "q"` selects keyless; `--engine all` selects all;
  `--list-engines` shows category column; unknown category → exit 2.
- **Files:** engine.go, engine_test.go, format.go, resolve.go, main_test.go,
  + 9 engine Descriptor() updates (brave/mojeek/ddg/wikipedia/grokipedia/
  arxiv/pubmed/hackernews/stackexchange).
- **Blocked on:** nothing
- **Next:** Tier C (scraped general web) — C.1.0 (ddg vqd multi-page).

## 2026-07-09 — arxiv reverted to `all:<q>` (matches SearXNG) + Sprint B.3.1 added

The initial AND-term fix was reverted after checking SearXNG's `arxiv.py` — it
uses the same `search_query=all:<q>` pattern. Our AND fix was a divergence from
the reference implementation. The architectural fix is engine categories
(Sprint B.3.1) — scoping arxiv behind `--engine academic`. The standard `all:<q>`
OR-flooding is acceptable for a science engine.

- **Done:**
  - `internal/engine/arxiv/arxiv.go`: reverted `buildRequestURL` back to
    `params.Set("search_query", "all:"+q)` — matches SearXNG exactly.
  - `internal/engine/arxiv/arxiv_test.go`: `wantQ` values reverted to
    `all:X Y Z` form; live test retains in-domain assertion, removed
    out-of-domain ≤5 check.
  - `TASKS.md`: added Stage 3 / Sprint B.3.1 (engine categories & aliases)
  - DECISIONS: old entry replaced with REVERTED note.
- **Verified:** `gofmt -l .` clean; `go build/vet` clean; `go test -race -count=1
  ./...` → 361 PASS, 6 SKIP; `MST_ITEST=1 arxiv TestSearch_Live` → PASS;
  `--engine arxiv "Erling Haaland diet"` → 30 OR'd results (expected, matches
  SearXNG).
- **Files:** arxiv.go, arxiv_test.go, TASKS.md, DECISIONS.md, PROGRESS.md.
- **Blocked on:** nothing
- **Next:** Sprint B.1.6 (`table` format + E2E), then B.3.1 (engine categories).

## 2026-07-09 — ddg enabled by default (overturns §17.3)

User decision after live testing showed ddg reliable while mojeek gets 403-blocked.

- **Done:**
  - `internal/engine/ddg/ddg.go`: `Enabled: false` → `true`; package doc updated.
  - `internal/engine/ddg/ddg_test.go`: TestDescriptor expects Enabled true.
  - `cmd/mst/main_test.go`: `listReg()` disabled-status example swapped from
    ddg → grokipedia (the engine still off by default); `TestRun_DDG_OffByDefault`
    repurposed → `TestRun_DDG_OnByDefault` (ddg runs without `-e`, mocked server);
    E2E comment updated.
  - **Spec:** §5 Mojeek note (now 2 keyless general-web engines), Tier C table
    ("On by default"), DDG note (rationale overturned), §6 config `enabled: true`,
    §16 roadmap, §17.3.
  - DECISIONS entry (overturns prior accepted decision).
- **Verified:** `gofmt -l .` empty; `go vet`/`go build` clean;
  `go test -race -count=1 ./...` → **361 tests green** (-race);
  `--list-engines` → ddg `enabled:true ready`; live default query (no `-e`)
  → ddg runs (count=10), top result "Erling Haaland's insane diet…".
- **Files:** ddg.go/test, cmd/mst/main_test.go, docs/SPEC.md, DECISIONS.md,
  PROGRESS.md.
- **Blocked on:** nothing
- **Next:** Sprint B.1.6 (`table` format + E2E), or implement `tavily` (Tier D,
  free tier confirmed).

## 2026-07-09 — Fix 3 live engine bugs (pubmed/hackernews/stackexchange) + ddg weight 1.0

A live all-engines query exposed that 3 "complete" engines failed against their
real endpoints (httptest-green, live-broken) — the same `base.Path`-replace
anti-pattern that hit grokipedia. Plus ddg weight bumped per user request.

- **Done:**
  - **hackernews:** `apiBase` host-only + `Path=/api/v1/search` (was `/search`, 404);
    test path assertions + apiBase overrides fixed; live test added.
  - **pubmed:** paths `/entrez/eutils/esearch.fcgi` + `/entrez/eutils/esummary.fcgi`
    (was 404). esearch stays XML (dropped `retmode=json`); **esummary switched
    XML→JSON** — the `<ArticleSet>` struct never matched live; new JSON
    `esummaryResponse{Result map[string]json.RawMessage}` + `docs()` + `parseResults([]esummaryDoc)`.
    Full test rewrite (JSON fixtures, `[]esummaryDoc` parse tests, path
    assertions); live test added.
  - **stackexchange:** `/2.3/search/advanced` (was versionless), `filter=withbody`
    (was invalid `!*236cL9uK5*(T6` → 400), `pagesize` (was `maxcount`). Test
    assertions fixed; live test added.
  - **ddg:** `DefaultWeight` 0.5→1.0 (descriptor + test + spec §6 config example).
  - DECISIONS entry (recurring `base.Path` pattern, per-engine fixes, ddg weight).
- **Verified:** `gofmt -l .` empty; `go vet`/`go build` clean;
  `go test -race -count=1 ./...` → **361 tests green** + 5 SKIP (live);
  `MST_ITEST=1 TestSearch_Live` green for pubmed/hackernews/stackexchange/ddg/grokipedia.
  Re-ran the all-engines misspelled query: pubmed ok(count=2), hackernews/stackexchange
  ok(count=0, expected), ddg ok(count=10) — ddg found the relevant Korean-cholesterol/
mortality papers (incl. "…in Korean adults") despite the "cholestral" typo.
- **Files:** pubmed.go/test, hackernews.go/test, stackexchange.go/test, ddg.go/test,
  docs/SPEC.md, DECISIONS.md, PROGRESS.md.
- **Blocked on:** nothing
- **Next:** commit; Sprint B.1.6 (`table` format + E2E).

## 2026-07-09 — Sprint B.1.5 review fixes (grokipedia)

Reviewed the grokipedia engine (implemented by a concurrent session, uncommitted)
against spec §5 + SearXNG `engines/grokipedia.py`. Found & fixed:

- **Done:**
  - **C1 path bug:** `buildRequestURL` dropped the `/api` segment (`base.Path =
    "/full-text-search"` on `apiBase="…/api"` — `url.Path` replaces, not
    appends → produced `grokipedia.com/full-text-search`). Tests asserted the
    wrong path, so passed while broken. Fixed: `apiBase` = host origin,
    `base.Path = "/api/full-text-search"`; both path assertions fixed.
  - **D1 html→text:** added `<[^>]*>` snippet stripper (§5 "html→text on snippet";
    matches stackexchange) + `TestParseResults_HTMLSnippet`.
  - **G1 live test:** added gated `TestSearch_Live` (MST_ITEST=1).
  - **H1/H2:** de-garbled the duplicated TASKS.md B.1.5 block; added DECISIONS
    entry; corrected test counts (358→360).
- **Verified:** `gofmt -l .` empty; `go vet`/`go build` clean;
  `go test -race -count=1 ./...` → 16 packages, **360 tests green** (+1 vs 359)
  + 2 SKIP (ddg/grokipedia live); `MST_ITEST=1 TestSearch_Live` → 10 real
  results, correct `/page/<slug>` URLs (`Go!_(…)` → `Go%21_%28…%29`), clean
  snippets; `--list-engines` shows `grokipedia` `enabled:false weight:1 disabled`.
- **Files changed:** `internal/engine/grokipedia/grokipedia.go`,
  `grokipedia_test.go`, `TASKS.md`, `DECISIONS.md`, `PROGRESS.md`.
- **Left (minor, per review):** G2 `limit>10` unverified (low risk, off-by-
  default engine); G3 retry coverage thinner than peers.
- **Blocked on:** nothing
- **Next:** commit B.1.5 (engine + fixes), then Sprint B.1.6 (`table` format).

## 2026-07-09 — ddg UA rotation (pulls forward §18)

Follow-up to the ddg redesign: replace the single static browser UA with a
rotating pool, per the user's request ("spoofed UA like a user is browsing").

- **Done:**
  - `internal/engine/ddg/ddg.go`: replaced `const ddgUA` with `browserUAs`
    (6 realistic desktop UAs: Chrome/Firefox/Safari/Edge across Windows/macOS/
    Linux) + `uaSource` (default: uniform random pick) + `SetUASelector` test
    hook. `fetch` picks `ua := uaSource()` once per Search call and reuses it
    for the request (forward-compatible with a future `vqd` multi-page sequence,
    which must reuse the same UA across a query's pages — vqd is query+UA-bound).
  - `internal/engine/ddg/ddg_test.go`: `TestSearch_Httptest` pins the UA via
    `SetUASelector` for a deterministic assertion; new `TestUASelection`
    (default selector returns a pool member; override + restore works).
  - **Spec:** §12 (carve-out now "rotated at random from a pool"); §5 (ddg note
    + access notes); §18 (scrape-UA rotation no longer deferred — only
    `MST_USER_AGENT` user-configurability remains deferred).
- **Verified:** `gofmt -l .` empty; `go vet`/`go build` clean;
  `go test -race -count=1 ./...` → 16 packages, **342 tests green** (+1
  TestUASelection); `MST_ITEST=1 TestSearch_Live` → 10 real results with a
  random pool UA.
- **Files changed:** `internal/engine/ddg/ddg.go`, `ddg_test.go`, `docs/SPEC.md`.
- **Blocked on:** nothing
- **Next:** Sprint B.1.5 — `grokipedia`

## 2026-07-09 — DDG redesign: SearXNG mechanism + §12 scrape-UA carve-out

Interlude sprint (user request): make `ddg` actually work by following
SearXNG's mechanism (`docs/DDG-research.md`).

- **Done:**
  - `internal/engine/ddg/ddg.go` — rewritten:
    - **Method:** form **POST** to `https://html.duckduckgo.com/html/`
      (`application/x-www-form-urlencoded`), body `q=<q>&b=&kl=wt-wt` (page 1)
    - **Headers (browser simulation):** static desktop UA + `Sec-Fetch-Dest:
      document`, `Sec-Fetch-Mode: navigate`, `Sec-Fetch-Site: same-origin`,
      `Sec-Fetch-User: ?1`, `Referer: https://html.duckduckgo.com/html/`,
      `Accept-Language` (no `Accept-Encoding` — let stdlib auto-decompress)
    - **Selectors (reverse-engineered, confirmed live):** container
      `#links div[class*=web-result]`; url+title `h2 a`; snippet `a.result__snippet`
    - **CAPTCHA detection:** `<form id="challenge-form">` → `ErrCaptcha`
      (post-fetch, not retried; surfaces as `ok:false` in the engines block)
    - **Query guard:** ≥500 chars → skip (no results, no error), matching SearXNG
    - **`uddg=` redirect unwrap:** `//duckduckgo.com/l/?uddg=<enc>` → real URL
    - **Single page** only; `vqd`-based page 2+ deferred (documented follow-up)
    - Retry (§11) via shared `internal/engine/retry`; `SearchWithRaw` retained
  - `internal/engine/ddg/ddg_test.go` — rewritten: 20 tests (was 13): descriptor,
    golden parse (normal/empty/no-results/uddg-unwrap/captcha), httptest
    round-trip (verifies POST, Content-Type, form body q/b/kl, browser headers),
    CAPTCHA-surfaces, query-too-long skip, buildRequestBody (2), error paths
    (cancelled/404), retry (429/5xx/exhausted/cancel-during-backoff/non-retryable),
    SearchWithRaw, + gated `TestSearch_Live` (MST_ITEST=1)
  - `cmd/mst/main_test.go` — `TestRun_DDG_E2E` fixture updated to the new markup
  - **Spec:** §5 DDG note (POST + browser headers + CAPTCHA + single-page); §5
    access notes (new `ddg` bullet with selectors/mechanics); §12 (scrape-UA
    carve-out — `ddg` browser UA, `mojeek` keeps honest); §18 (UA wording)
  - **DECISIONS.md:** new 2026-07-09 entry; prior "fidelity unverified" entry
    marked resolved
- **Verified:**
  - `gofmt -l .` — empty; `go vet ./...` — clean; `go build ./...` — ok
  - `go test -race -count=1 ./...` → **16 packages, 341 tests green** (was 337,
    +4 net: ddg 13→20 = +7, cmd/mst E2E fixture fix, −3 from removed old tests)
  - `MST_ITEST=1 go test -run TestSearch_Live ./internal/engine/ddg/` → **PASS**:
    10 real results, first `"The Go Programming Language" <https://go.dev/>`,
    selectors confirmed against live markup, URLs clean (no `uddg=`/`/l/`)
  - Binary smoke: `/tmp/mst --engine ddg --limit 5 "go concurrency"` → exit 0,
    10 ranked results, `engines.ddg.ok=true count=10`, sources derived correctly
- **Blocked on:** nothing
- **Next:** Sprint B.1.5 — `grokipedia` (next planned Tier B engine). DDG
  `vqd`/multi-page is a documented follow-up (DECISIONS 2026-07-09).

## 2026-07-08 — Sprint B.1.4: `hackernews` (keyless Algolia API)

The fourth Tier B knowledge engine: Hacker News Algolia Search API, returning
structured JSON for HN stories.

- **Done:**
  - `internal/engine/hackernews/hackernews.go` — Hacker News Algolia Search API client
    - HTTP GET to `https://hn.algolia.com/api/v1/search` with params: `query`,
      `tags=story`, `hitsPerPage`
    - Parses JSON response into `[]engine.Result`
    - Maps: `title` → title, `url` → URL (or constructed from `objectID` via
      `https://news.ycombinator.com/item?id=<id>`), snippet = date + points
    - `buildRequestURL`: sets `query`, `tags=story`, `hitsPerPage` (capped at
      1000, floored at 1)
    - Retry policy (§11): via shared `internal/engine/retry` package
  - `internal/engine/hackernews/hackernews_test.go` — 17 tests:
    - **Descriptor:** Name/keyed=false/DefaultWeight=0.8/Enabled=true
    - **Golden-table parse:** Normal (2 results), empty, missing-URL,
      missing-title, construct-URL-from-objectID, malformed JSON
    - **httptest round-trip:** Verifies headers, parses fixture JSON into `[]Result`
    - **Error paths:** Context cancelled, HTTP 502, malformed JSON
    - **buildRequestURL:** 3 cases (normal, cap at 1000, floor at 1)
    - **Retry:** 429 succeeds on retry, exhausted retries
  - Wire into `defaultRegistry()` in `cmd/mst/main.go`
- **Verified:**
  - `go build ./...` — clean
  - `go vet ./...` — clean
  - `gofmt -l .` — empty
  - `go test -race -count=1 ./...` → **15 packages, 337 tests green** (was 320,
    +17: +17 hackernews tests)
  - `/tmp/mst --list-engines` → shows `hackernews` with status `ready`
- **Blocked on:** nothing
- **Next:** Sprint B.1.5 — `grokipedia`

## 2026-07-08 — Sprint B.1.3: `stackexchange` (keyless Stack Exchange API)

The third Tier B knowledge engine: Stack Exchange Search API, returning structured
JSON with HTML body content.

- **Done:**
  - `internal/engine/stackexchange/stackexchange.go` — Stack Exchange Search API client
    - HTTP GET to `https://api.stackexchange.com/2/search/advanced`
    - Parses JSON response into `[]engine.Result`
    - Maps: `question_id` → URL (`https://stackoverflow.com/questions/<id>`),
      `title` → title, `link` → URL, `body` → snippet (HTML tag stripped)
    - `buildRequestURL`: sets `q`, `site=stackoverflow`, `order=desc`,
      `sort=relevance`, `maxcount` (capped at 100, floored at 1)
    - Retry policy (§11): via shared `internal/engine/retry` package
    - HTML tag stripping in `stripNewlines` — Stack Exchange body is raw HTML
  - `internal/engine/stackexchange/stackexchange_test.go` — 16 tests:
    - **Descriptor:** Name/keyed=false/DefaultWeight=1.0/Enabled=true
    - **Golden-table parse:** Normal (2 results), empty, missing-link, missing-title,
      malformed JSON
    - **httptest round-trip:** Verifies headers, parses fixture JSON into `[]Result`
    - **Error paths:** Context cancelled, HTTP 502, malformed JSON, API error response
    - **buildRequestURL:** 3 cases (normal, cap at 100, floor at 1)
    - **Retry:** 429 succeeds on retry, exhausted retries
  - Wire into `defaultRegistry()` in `cmd/mst/main.go`
- **Verified:**
  - `go build ./...` — clean
  - `go vet ./...` — clean
  - `gofmt -l .` — empty
  - `go test -race -count=1 ./...` → **14 packages, 320 tests green** (was 303,
    +17: +16 stackexchange tests + 1 from wiring into main)
  - `/tmp/mst --list-engines` → shows `stackexchange` with status `ready`
- **Blocked on:** nothing
- **Next:** Sprint B.1.4 — `hackernews`

## 2026-07-08 — Sprint B.1.2: `pubmed` (keyless E-utilities API)

The second Tier B knowledge engine: PubMed's NCBI E-utilities API, which uses
a two-step XML flow (esearch → esummary).

- **Done:**
  - `internal/engine/pubmed/pubmed.go` — PubMed E-utilities API client
    - HTTP GET to `https://eutils.ncbi.nlm.nih.gov/entrez/eutils/esearch.fcgi`
      with params: `db=pubmed`, `term=<q>`, `retmax=<limit>`, `retmode=json`
    - HTTP GET to `https://eutils.ncbi.nlm.nih.gov/entrez/eutils/esummary.fcgi`
      with params: `db=pubmed`, `id=<comma-separated-PMIDs>`, `retmode=json`
    - Parses XML responses into `[]engine.Result`
    - Maps `<Id>` → PubMed URL (`https://pubmed.ncbi.nlm.nih.gov/<id>/`)
    - Extracts: `<title>` → title, `<Source>` + `<PubDate>` → snippet
    - `buildSearchURL`: sets `term`, `db=pubmed`, `retmax` (capped at 10000,
      floored at 1)
    - `buildSummaryURL`: joins PMIDs with commas for the `id` param
    - Retry policy (§11): via shared `internal/engine/retry` package
  - `internal/engine/pubmed/pubmed_test.go` — 17 tests:
    - **Descriptor:** Name/keyed=false/DefaultWeight=1.2/Enabled=true
    - **Golden-table parse:** Normal (2 results), empty, missing-id, missing-title,
      malformed XML
    - **httptest round-trip:** Verifies esearch path + esummary path, parses
      fixture XML into `[]Result`
    - **Error paths:** Context cancelled, HTTP 502, malformed XML, esearch returns empty
    - **buildSearchURL:** 3 cases (normal, cap at 10000, floor at 1)
    - **buildSummaryURL:** Verifies path, db, id params
    - **Retry:** 429 succeeds on retry, exhausted retries
  - Wire into `defaultRegistry()` in `cmd/mst/main.go`
- **Verified:**
  - `go build ./...` — clean
  - `go vet ./...` — clean
  - `gofmt -l .` — empty
  - `go test -race -count=1 ./...` → **14 packages, 303 tests green** (was 276,
    +27: +17 pubmed tests + 10 from wiring into main)
  - `/tmp/mst --list-engines` → shows `pubmed` with status `ready`
- **Blocked on:** nothing
- **Next:** Sprint B.1.3 — `stackexchange`

## 2026-07-08 — Sprint B.1.1: `arxiv` (keyless Atom XML API)

The first Tier B engine: keyless structured API that returns Atom XML.

- **Done:**
  - `internal/engine/arxiv/arxiv.go` — arXiv API client
    - HTTP GET to `https://export.arxiv.org/api/query`
    - Parses Atom XML response into `[]engine.Result`
    - Extracts: `<id>` → URL (`https://arxiv.org/abs/<id>`), `<title>`, `<summary>`
    - `buildRequestURL`: sets `search_query=all:<q>`, `start=0`, `max_results` (capped at 3000, floored at 1)
    - Retry policy (§11): via shared `internal/engine/retry` package
  - `internal/engine/arxiv/arxiv_test.go` — 14 tests:
    - **Descriptor:** Name/keyed=false/DefaultWeight=1.2/Enabled=true
    - **Golden-table parse:** Normal (2 results), empty, missing-alternate-link
    - **httptest round-trip:** Verifies headers, parses fixture XML into `[]Result`
    - **Error paths:** Context cancelled, HTTP 502, malformed XML
    - **buildRequestURL:** 3 cases (normal, cap at 3000, floor at 1)
    - **Retry:** 429 succeeds on retry, exhausted retries
  - Wire into `defaultRegistry()` in `cmd/mst/main.go`
- **Verified:**
  - `go build ./...` — clean
  - `go vet ./...` — clean
  - `gofmt -l .` — empty
  - `go test -race -count=1 ./...` → **13 packages, 276 tests green** (was 262, +14)
  - `/tmp/mst --list-engines` → shows `arxiv` with status `ready`
- **Blocked on:** nothing
- **Next:** Sprint B.1.2 — `pubmed`

## 2026-07-08 — Sprint A.2.2 (partial): fuzz/property tests + `--list-engines`

Implemented the two unambiguous A.2.2 DoD items; surfaced the remaining
v0.1-scope items as an open decision rather than silently expanding/dropping.

- **Done:**
  - **§14 fuzz/property tests** (`internal/rank/fuzz_test.go`,
    `property_test.go`):
    - `FuzzCanonicalize` — seeds §9 equivalence classes + malformed inputs;
      asserts no-panic, idempotency, fragment-free. Idempotency scoped to
      realistic engine URLs (http/https, simple hostname, ≤1 leading www);
      degenerate inputs (colon hosts, non-http schemes, multi-www) assert
      determinism only, matching Canonicalize's best-effort fallback contract.
      Verified: 60s / 4.4M execs / 0 failures. The fuzz run surfaced 3 real
      pathological non-idempotent inputs (`"//0 "`, `"A0000://:?A"`,
      `"//://0"`) — characterized + excluded by scope.
    - Property tests (deterministic-seeded): run-reordering determinism
      (60×8), single-engine score monotonicity (+ exact w/(r+k)),
      dedup-sums-contributions, all-scores-positive.
  - **§4 `--list-engines` full output** (replaces A.0.2 placeholder):
    - `format.ListEngine` + `format.MarshalListEngines` (json/jsonl/text;
      nil→`[]`; table→error).
    - `resolve.computeStates` extracted from `resolvePlan` — shared state
      resolution (§4 selection + §6 precedence); `resolve.computeListEntries`
      maps states → `ListEngine` (`key_present` null for keyless).
    - `main.run` wires `--list-engines` before query resolution (exit 0);
      `validateListFormat` (table→exit 2).
    - Tests: 7 in `cmd/mst` (JSON fields/status/key_present-null, key-present
      flip, jsonl, text, table→exit2, `--weight` reflected, no-query-needed) +
      6 in `format` (json/jsonl/text/empty/table-reject/nil).
- **Verified:** `gofmt -l .` empty; `go vet ./...` clean; `go build ./...` ok;
  `go test -race -count=1 ./...` → 12 packages, **241 tests green** (was 206,
  +35: +22 rank fuzz/property, +13 list-engines). Binary smoke: `--list-engines`
  json/jsonl/text all match §4; `BRAVE_API_KEY` flips brave missing-key→ready;
  `-f table` → exit 2.
- **Files changed:** `internal/rank/fuzz_test.go` (new),
  `internal/rank/property_test.go` (new), `internal/format/format.go`,
  `internal/format/format_test.go`, `cmd/mst/main.go`, `cmd/mst/main_test.go`,
  `cmd/mst/resolve.go`, `TASKS.md`, `DECISIONS.md`.
- **Blocked on:** decision on remaining v0.1 items (`--no-merge`/`--raw`/
  `--help` tier grouping) — see DECISIONS 2026-07-08 part 2.
- **Next:** per the decision — implement `--no-merge` + `--help` grouping
  (recommended) then close A.2.2 and tag v0.1; or close A.2.2 now with
  `--raw`/`--no-merge` re-deferred.

## 2026-07-08 — Sprint A.2.1 review follow-up (P2 fixes + DDG risk)

Implemented the P2 action items from the A.2.1 code review and logged the DDG
real-world-fidelity risk.

- **Done:**
  - **P2 (test speed + consistency):** `ddg_test.go` and `wikipedia_test.go`
    now have the `fastRetry(t)` helper (matching brave/mojeek) and call it in
    every retry test. The `TestSearch_Retry_ContextCancelledDuringBackoff`
    tests in both were rewritten to the canonical brave/mojeek form (no
    `Retry-After: 3600`; `context.WithCancel` + goroutine + 20ms) so they stay
    valid under the zeroed backoff. Measured: ddg pkg 4.9s→1.08s, wikipedia
    pkg 5.3s→1.07s; full suite ~12s→~3.4s.
  - **P2 (wikipedia malformed-payload handling):** `parseResults` now returns
    `([]engine.Result, error)` and surfaces per-element `json.Unmarshal`
    errors (and a `<4-elements` error) instead of silently discarding them.
    Previously a valid-JSON-array response with non-string inner elements
    (e.g. a schema change) flattened to `ok:true/count:0`; it now returns a
    `malformed response` error. New regression test
    `TestParseResults_MalformedInner`; `TestParseResults_TooFewArrays` updated
    to expect an error; the 4 happy-path golden tests check `err`.
  - **P3 (DDG-fidelity risk):** logged in DECISIONS.md — `ddg` selectors/URLs
    are unverified against the live `html.duckduckgo.com/html/` markup and
    result URLs are not `uddg=`/`/l/` redirect-unwrapped; track via an
    `MST_ITEST=1` live test before promoting ddg. No code change (consistent
    with §5 "best-effort, fragile").
- **Verified:** `gofmt -l .` empty; `go vet ./...` clean; `go build ./...` ok;
  `go test -race -count=1 ./...` → 12 packages ok, **206 tests green** (was 205
  pre-fix +1 `MalformedInner`). Note: the prior A.2.1 entry's "217" was
  inaccurate (measured 205 then, 206 now).
- **Files changed:** `internal/engine/wikipedia/wikipedia.go`,
  `internal/engine/wikipedia/wikipedia_test.go`,
  `internal/engine/ddg/ddg_test.go`, `DECISIONS.md`.
- **Blocked on:** nothing.
- **Next:** Sprint A.2.2 — hardening (fuzz/property tests, `--list-engines`,
  exit/format polish).

## 2026-07-08 — Sprint A.2.1: `wikipedia` + `ddg`

Implemented both remaining v0.1 engines.

- **Done:**
  - **`wikipedia`** (`internal/engine/wikipedia/`):
    - `wikipedia.go` — Wikipedia OpenSearch API client
      - HTTP GET to `https://en.wikipedia.org/w/api.php?action=opensearch`
      - Parses JSON response `[query, [titles], [descriptions], [urls]]` into `[]engine.Result`
      - Limit capped at 20, floored at 1 (Wikipedia API limits)
      - Retry via shared `internal/engine/retry` package
    - `wikipedia_test.go` — 16 tests:
      - **Descriptor:** Name/keyed/weight/enabled
      - **Golden-table parse:** Normal (2 results), empty, skip-empty-URL, mismatched-lengths, too-few-arrays
      - **httptest round-trip:** Verifies headers, parses fixture into `[]Result`
      - **buildRequestURL:** 3 cases (normal, cap at 20, floor at 1)
      - **Error paths:** Context cancelled, HTTP 404, malformed JSON
      - **Retry:** 429 succeeds on retry, exhausted retries, 5xx succeeds, context cancelled during backoff, non-retryable status
  - **`ddg`** (`internal/engine/ddg/`):
    - `ddg.go` — DuckDuckGo HTML scrape engine
      - HTTP GET to `https://html.duckduckgo.com/html/?q=<q>`
      - goquery HTML parsing with selectors per DDG lite template:
        - container: `div.result`
        - url: `a.result__a/@href`
        - title: `h2.result__title`
        - snippet: `.result__snippet`
      - `Enabled: false` by default (§17.3) — opt-in via `--engine ddg`
      - Retry via shared `internal/engine/retry` package
    - `ddg_test.go` — 13 tests:
      - **Descriptor:** Name/keyed/weight/enabled (false)
      - **Golden-table parse:** Normal (2 results), empty, no-results-standard
      - **httptest round-trip:** Verifies headers, parses fixture HTML into `[]Result`
      - **buildRequestURL:** 2 cases (normal, spaces)
      - **Error paths:** Context cancelled, HTTP 404
      - **Retry:** 429/5xx succeed on retry, exhausted, context cancelled during backoff, non-retryable
  - **E2E tests** in `cmd/mst/main_test.go`:
    - `TestRun_Wikipedia_E2E` — full pipeline with httptest server
    - `TestRun_DDG_E2E` — full pipeline with httptest server (explicit `--engine ddg`)
    - `TestRun_DDG_OffByDefault` — verifies ddg is disabled by default, enabled with `--engine`
  - Wire into `defaultRegistry()` in `cmd/mst/main.go`
- **Verified:**
  - `go build ./...` — clean
  - `go vet ./...` — clean
  - `gofmt -l .` — empty
  - `go test -race -count=1 ./...` → **217 tests green** (was 168, +49)
  - `/tmp/mst --list-engines` → shows `brave`, `mojeek`, `wikipedia`, `ddg`
- **Blocked on:** nothing
- **Next:** Sprint A.2.2 — hardening

<!--
Entry template:

## YYYY-MM-DD — <active Tier / Stage / Sprint>
- Done: …
- In progress: …
- Verified: … (commands run / test output)
- Blocked on: …
- Next: …
-->

## 2026-07-08 — REVIEW.md follow-up: shared retry package + bug fixes

Implemented all REVIEW.md action items (P1/P2/P3) plus 3 additional bugs found
while extracting the retry loop. Full detail in REVIEW.md (Resolution section)
and DECISIONS.md.

- **Done:**
  - **P2:** `internal/engine/retry/retry.go` — `Do(ctx, engineName, newReq)`
    encapsulates the whole §11 retry loop; both engines call it. 19 tests.
    Deduplication complete (grep confirms no leftover helpers in engines).
  - **Bug (double-sleep, both engines):** HTTP-error path slept twice per
    retry via `retryable()` + `sleepCtx()`. Fixed → single sleep. Brave test
    pkg: ~10s → ~1s.
  - **Bug (connection leak, mojeek):** `defer resp.Body.Close()` inside the
    pagination loop held N bodies open. Fixed via `parseAndClose` (no defer in
    loop).
  - **Bug (Retry-After semantics, both engines):** `min(Retry-After, backoff)`
    ignored the server's wait when it exceeded backoff. Fixed →
    `max(Retry-After, backoff)` per spec §11 (context deadline is the real cap).
  - **P1 (mojeek data loss):** mid-pagination failure now returns
    `(allResults, lastErr)` (partial results + error) per updated §7.
  - **P3:** no defer-in-loop; `retry.SetBackoffOverride`/`SetJitterSource`
    exported; weak `TestSearch_Pagination` → 3 focused tests.
  - **Spec updates:** §7 (partial-results contract), §11 (centralized retry +
    Retry-After max() semantics + network-error retry + non-retryable
    immediate return), §15 (`engine/retry/` in layout).
- **Verified:** `go build`/`go vet` clean; `gofmt -l .` empty;
  `go test -race -count=1 ./...` → **168 tests green** (was 147), 10 packages.
- **Blocked on:** nothing.
- **Next:** Sprint A.2.1 — `wikipedia` + `ddg`

## 2026-07-08 — Sprint A.1.2 follow-up: review of A.1.1 + A.1.2 code

Reviewed all source and test code from Sprints A.1.1 (`brave`) and A.1.2 (`mojeek`)
against spec requirements (§5, §8, §11, §12). Findings documented in `REVIEW.md`.

- **Findings:**
  - Both engines are spec-compliant and thoroughly tested (green build + 147 tests)
  - **Bug found:** mojeek multi-page data loss — if page N succeeds but page N+1
    exhausts retries, `Search()` returns `nil, lastErr` discarding accumulated results
  - **DRY concern:** retry helpers duplicated across both engines (6 functions each)
  - Minor issues: `defer` inside retry for-loop, missing accessor functions for test hooks
- **Verified:** `go build`, `go vet`, `gofmt -l .` all clean; `go test -race -count=1 ./...`
  green (9 packages).
- **Blocked on:** nothing — findings are logged, not blocking A.2.1.
- **Next:** Sprint A.2.1 — `wikipedia` + `ddg`

## 2026-07-08 — Sprint A.1.2 — `mojeek` (keyless HTML scrape)

The second Tier A engine: keyless HTML scrape with goquery.

- **Done:**
  - `internal/engine/mojeek/mojeek.go` — Mojeek Search API client
    - HTTP GET to `https://www.mojeek.com/search?q=<q>&safe=0`
    - Pagination: up to 10 pages (`s` param, omit on page 1 to avoid rate-limit)
    - goquery HTML parsing with selectors per spec §5:
      - container: `ul.results-standard > li`
      - url: `li > a.ob/@href`
      - title: `li > h2` text
      - snippet: `li > p.s` text
    - Browser DOM quirk: `<a class="ob">` closes before `<h2>` (HTML nesting),
      so selectors traverse from `li`, not from `a.ob`
    - Retry policy (§11): up to 2 retries on HTTP 429/5xx, exponential backoff
      (base 500ms, factor 2), ±20% jitter, honors Retry-After
    - Test hooks: `backoffOverride`/`jitterSource` for deterministic tests
  - `internal/engine/mojeek/mojeek_test.go` — 15 tests:
    - **Golden-table:** `TestParseResults` (3 results), `TestParseResults_Empty`,
      `TestParseResults_NoResultsStandard` — pure function, no I/O
    - **httptest round-trip:** `TestSearch_Httptest` — verifies headers,
      parses fixture HTML into `[]Result`
    - **Error paths:** `TestSearch_ContextCancelled`, `TestSearch_HTTPError` (404)
    - **Descriptor:** `TestDescriptor` — Name/Keyed=false/DefaultWeight=1.3/Enabled
    - **buildRequestURL:** `TestBuildRequestURL` — 3 cases (page 0 no s, page 1 s=10, page 5 s=50)
    - **Pagination:** `TestSearch_Pagination` — verifies multi-page fetching
    - **Retry:** `TestSearch_Retry_429SucceedsOnRetry`,
      `TestSearch_Retry_5xxSucceedsOnRetry`, `TestSearch_Retry_Exhausted`,
      `TestSearch_Retry_ContextCancelledDuringBackoff`,
      `TestSearch_Retry_NonRetryableStatus`, `TestSearch_Retry_RetryAfterHonored`
  - Wire into `defaultRegistry()` in `cmd/mst/main.go`
  - Added `github.com/PuerkitoBio/goquery` + `github.com/andybalholm/cascadia` deps
- **Verified:**
  - `go build ./...` — clean
  - `go vet ./...` — clean
  - `gofmt -l .` — empty
  - `go test -race ./...` — 147 tests green (was 129, +18 mojeek tests)
  - `/tmp/mst --list-engines` → `brave`, `mojeek`
  - `/tmp/mst "go programming"` → exit 1 (403 from live site — expected for datacenter IPs)
- **Blocked on:** nothing
- **Next:** Sprint A.2.1 — `wikipedia` + `ddg`

## 2026-07-07 — Sprint A.1.1 follow-up: retry logic (§11)

Added per-engine retry policy to the brave Search() method.

- **Done:**
  - `internal/engine/brave/brave.go` — retry loop in `Search()`:
    - Up to 2 retries on HTTP 429/5xx (exponential backoff: base 500ms, factor 2)
    - ±20% jitter via `jitter()` function (configurable via `jitterSource` for tests)
    - Honors `Retry-After` header when present (uses min of Retry-After and calculated backoff)
    - Context cancellation during backoff sleep aborts the retry loop promptly
    - Non-retryable status codes (400/401/403) return immediately without retry
    - Network/DNS errors are also retried (treated identically to timeouts per §11)
    - Test hooks: `backoffOverride` for deterministic backoff, `jitterSource` for deterministic jitter
  - `internal/engine/brave/brave_test.go` — 6 new tests:
    - `TestSearch_Retry_429SucceedsOnRetry` — transient 429 → retry → success (2 requests)
    - `TestSearch_Retry_5xxSucceedsOnRetry` — transient 500 → retry → success
    - `TestSearch_Retry_Exhausted` — persistent 429 → exhaust retries → error with 429 in message
    - `TestSearch_Retry_ContextCancelledDuringBackoff` — cancel during backoff → returns promptly
    - `TestSearch_Retry_NonRetryableStatus` — 401 returns immediately, no retry
    - `TestSearch_Retry_RetryAfterHonored` — Retry-After header respected
    - Updated `TestSearch_HTTPError` to verify 3 total requests (initial + 2 retries)
- **Verified:**
  - `go build ./...` — clean
  - `go vet ./...` — clean
  - `gofmt -l .` — empty
  - `go test -race ./...` — 129 tests green (was 123, +6 retry tests)
- **Blocked on:** nothing
- **Next:** Sprint A.1.2 — `mojeek` (keyless HTML scrape)

## 2026-07-07 — Sprint A.1.1 — `brave` engine

The first real search engine implementation.

- **Done:**
  - `internal/engine/brave/brave.go`: Brave Search API client
    - HTTP GET to `https://api.search.brave.com/res/v1/web/search`
    - Minimal JSON response types (`apiResponse`, `webResponse`, `apiResult`)
      matching the Brave API schema — only deserializes fields we need
    - `buildRequestURL`: sets `q` and `count` params (capped at 20, floored at 1)
    - Headers: `Accept: application/json`, `X-Subscription-Token: <API_KEY>`,
      `User-Agent: mst/dev`
    - Error handling: HTTP status >= 400 → error with body; parse failure → error;
      context cancellation → error
    - `New()` returns a singleton `engine.Engine` implementation
  - `internal/engine/brave/brave_test.go`: 10 tests
    - **Golden-table:** `TestParseResults` (2 results), `TestParseResults_Empty`,
      `TestParseResults_SkipEmptyURL` — pure function, no I/O
    - **httptest round-trip:** `TestSearch_Httptest` — verifies headers (Accept,
      User-Agent, X-Subscription-Token), parses fixture JSON into `[]engine.Result`
    - **Error paths:** `TestSearch_NoKey`, `TestSearch_ContextCancelled`,
      `TestSearch_HTTPError` (429), `TestSearch_MalformedResponse` — all return
      nil results + non-nil error
    - **Descriptor:** `TestDescriptor` — verifies Name/Keyed/KeyEnv/DefaultWeight/Enabled
    - **buildRequestURL:** `TestBuildRequestURL` — 4 cases (normal, cap at 20,
      min 1, floor at 1)
  - E2E test: `TestRun_Brave_E2E` in `cmd/mst/main_test.go` — real brave engine
    through full pipeline (dispatch → rank → format) with httptest server
  - Wire into `defaultRegistry()` in `cmd/mst/main.go`
  - `apiBase` changed from `const` to `var` for test override; added
    `SetAPIBase`/`APIBase` accessor functions

- **Verified:**
  - `go build ./...` — clean
  - `go vet ./...` — clean
  - `gofmt -l .` — empty (no formatting issues)
  - `go test -race ./...` — 123 tests green (was 112, +11)
  - `/tmp/mst --list-engines` → `brave`
  - `/tmp/mst "go concurrency"` → exit 1, auto-skips without `BRAVE_API_KEY`
  - `BRAVE_API_KEY=test /tmp/mst "go concurrency"` → exit 1 (invalid key),
    engines block shows `brave.ok=false, error="brave: HTTP 422: ..."`
  - All existing tests pass (no regressions from registering brave)

- **Blocked on:** nothing
- **Next:** Sprint A.1.2 — `mojeek` (keyless HTML scrape)

## 2026-07-07 — Sprint A.0.1 — Scaffold + config + engine registry

- **Done:**
  - `go.mod` with module `github.com/openmtx/meta-search-tool` + `gopkg.in/yaml.v3` dep
  - Project layout per §15: `cmd/mst/`, `internal/{config,engine,search,rank,cli,format}`, `testdata/`
  - `internal/engine/engine.go`: `Engine` interface, `Result` struct, `Options`, `EngineInfo`, `Registry` with Register/Get/All/Names methods, `Status` enum (ready/missing-key/disabled)
  - `internal/config/config.go`: config discovery (XDG_CONFIG_HOME / ~/.config), YAML load via yaml.v3, version enforcement (only v1 accepted), precedence merge (defaults → file → env → CLI)
  - `internal/cli/cli.go`: flag parsing with `flag` stdlib — all Tier A1 flags (`--format`, `--limit`, `--offset`, `--timeout`, `--rrf-k`, `--stdin`, `--config`, `--list-engines`, `--version`, `--strict`, `--no-merge`, `--raw`, `--engine`, `--exclude`, `--require`, `--weight` repeatable, `--verbose`)
  - `cmd/mst/main.go`: entry point wiring flags → config → query resolution → short-circuit paths (`--version`, `--list-engines`)
  - Unit tests: config (4 tests: defaults-only, version enforcement, malformed YAML, valid config), engine registry (5 tests: register/get, duplicate, all/names, status strings), CLI (6 tests: basic flags, --version, --list-engines, --weight, stdin+positional conflict, splitComma)

- **Verified:**
  - `go build ./...` — clean
  - `go test ./...` — all green (14 tests across 3 packages: cli 6, config 4, engine 4)
  - `./mst --version` → `mst dev` (exit 0)
  - `./mst --list-engines` → lists engines (exit 0)
  - `./mst "hello world"` → prints resolved query (exit 0)
  - `./mst` (no query) → error message, exit 2
  - `echo "" | ./mst --stdin` → empty query error, exit 2
  - `echo "test" | ./mst --stdin` → works (exit 0)

- **Blocked on:** decision on short-flag parser (see DECISIONS.md)
- **Next:** Sprint A.0.2 — dispatcher (fan-out, concurrency cap), RRF scoring + dedup + sort, formatters (json/jsonl/text), structured errors + exit codes

## 2026-07-07 — Sprint A.0.1 review fixes

Addressed the review findings on the initial A.0.1 build before committing it.

- **Done:**
  - `Engine.Search` now takes `context.Context` per spec §8 (was missing —
    would have blocked the A.0.2 per-engine timeout model, §7/§11).
  - `--weight` custom `flag.Value` no longer panics on usage errors (guarded nil
    receiver in `String()`; Go's `flag` reflects a zero value and calls
    `String()` during `isZeroValue`). Non-numeric `--weight ENGINE=X` is now a
    usage error → exit 2 (was silently coerced to `0.0`).
  - Registered `--per-engine-timeout` (the `Flags` field existed but the flag
    was unwired).
  - `Registry` now preserves registration order and exposes `Infos()` (calls
    `Descriptor()` per engine) — satisfies the DoD "registry enumerates via
    Descriptor()" and gives the ranker deterministic ordering (§9).
  - Empty/whitespace positional query now yields "empty query" (exit 2),
    distinct from the no-query case.
  - `go.mod`: `yaml.v3` correctly a direct dep (was `// indirect`); `gofmt -w`
    applied (4 files had misaligned struct fields).

- **Decision (recorded in DECISIONS.md, folded into spec §6):** config discovery
  uses `MST_CONFIG_HOME` (points directly at the mst config dir, like
  `DOCKER_CONFIG`), not `XDG_CONFIG_HOME`. Default fallback unchanged
  (`~/.config/mst/config.yaml`).

- **Decision (proposed in DECISIONS.md, awaiting answer):** short flags
  `-e/-n/-f/-q` and `-v/-vv` need a non-stdlib parser (stdlib `flag` can't do
  short/long aliases or count flags). Still unimplemented — flagged, not silent.

- **Verified:**
  - `gofmt -l .` → empty
  - `go build ./...`, `go vet ./...` → clean
  - `go test ./...` → 18 tests green (cli 8, config 5, engine 5);
    `go test -v ./... | grep -c '--- PASS'` = 18
  - `./mst --weight brave=abc q` → exit 2, stderr `invalid weight "abc" for
    engine "brave"...`, nothing on stdout
  - `./mst ""` → exit 2, stderr `empty query`
  - `./mst -e brave q` → no panic (clean `flag provided but not defined`)
  - `./mst --per-engine-timeout 3s q` → exit 0

## 2026-07-07 — Sprint A.0.1 (cont.): pflag swap + module path fix

Two follow-up commits after the review-fix commit, both before A.0.2.

- **Done:**
  - Switched the flag parser from stdlib `flag` to `github.com/spf13/pflag`
    (decision 2026-07-07, accepted) so the spec-§4 short aliases (`-e`, `-n`,
    `-f`) and repeatable count flag (`-v`/`-vv` via `CountVarP`) work. `--weight`
    now uses `StringArrayVarP`, so the custom `flag.Value` (and its `isZeroValue`
    panic) is gone entirely. `--help` returns `cli.ErrHelp` → main exits 0.
  - Removed `resolveFormat`'s silent `--raw` coercion, which had masked
    `validateFormat`; now `--raw --format text|table|jsonl` is a usage error →
    exit 2 (spec §10).
  - Fixed the module path `github.com/earendil-works/...` →
    `github.com/openmtx/...` to match the git remote (needed for `go install`).
  - `docs/SPEC.md` §3 now names pflag; §6 has `MST_CONFIG_HOME`.

- **Verified:**
  - `gofmt -l .` → empty; `go build ./...`, `go vet ./...` → clean
  - `go test ./...` → 21 tests green (cli 11, config 5, engine 5);
    `go test -v ./... | grep -c '--- PASS'` = 21
  - `./mst -e brave,ddg -n 5 -f jsonl q` → engines=[brave ddg], limit=5,
    format=jsonl; `./mst -vv q` → verbose=2; `./mst --help` → exit 0;
    `./mst --raw --format text q` → exit 2 (§10); `./mst --bogus q` → 0 panic lines
  - `./mst --version` → `mst dev`; module path resolves as openmtx

- **Blocked on:** nothing
- **Next:** Sprint A.0.2 — dispatcher (fan-out, concurrency cap), RRF + dedup +
  sort, formatters (json/jsonl/text), structured errors + exit codes.

## 2026-07-07 — Sprint A.0.2 — Dispatch + ranking + output + errors

The merged-result pipeline (§4/§7/§9/§10/§11). Four logical commits:

- **Done:**
  - `engine.EngineRun` (§8 sketch) added — dispatcher-assembled per-engine record
    consumed by the ranker.
  - `internal/rank` (all pure): `Canonicalize` (§9 step-3 URL canonicalization —
    lowercase scheme/host, drop default ports, strip fragment/trailing-slash,
    strip tracking params w/o reordering or re-encoding, drop leading
    `www[digits]`); `SourceFromURL` (eTLD+1 via `golang.org/x/net/publicsuffix`,
    §17.13); `Rank` (weighted RRF §9 steps 1-2, canonical-URL dedup/merge, snippet
    rule, deterministic final sort — score/#engines/top-reg/canonical); `Window`
    (§9 step-5 `[offset,offset+limit)` + 1-indexed ranks); `FetchLimit`
    (`max((offset+limit)*3,20)`).
  - `internal/search` dispatcher (§7): concurrent fan-out, `ConcurrencyCap=8`
    semaphore (§17.12), per-engine `context.WithTimeout` nested under the global
    ctx (`min` is automatic), panic recovery, registration-order output.
  - `internal/format` (§10/§11): `Envelope`/`EngineStat`, `BuildEnvelope`,
    `Marshal(json|jsonl|text)` (compact), `MarshalError`/`StructuredError`
    (`{error:{code,message,exit}}`), `errString` normalizes
    `context.DeadlineExceeded`→`"timeout"`.
  - `cmd/mst`: `run(args,stdin,stdout,stderr,reg)->int` testable core; `resolvePlan`
    (§4 selection + §6 precedence: weights default→config→flag, timeouts
    config→flag→`min(global,6s)`, keyed auto-skip, unknown-name / require∩exclude /
    require-cannot-start → exit 2); `computeExit` (§4 precedence 2>3>1>0, exit 3
    beats 1); `validateFormat` (json/jsonl/text now; table→v0.2, --raw/--no-merge→
    A.2.x surface clear exit-2 errors).
  - `internal/cli`: `Parse(args, out)` — explicit args + usage writer, pflag output
    suppressed so stderr is always a single §11 object; `--help`→exit 0.

- **Verified:**
  - `gofmt -l .` → empty; `go build/vet ./...` → clean
  - `go test -race ./...` → clean; `go test -v ./... | grep -c '--- PASS'` = 112
    (cli 11, config 5, engine 5, rank 42, search 8, format 9, cmd/mst 32)
  - DoD met: golden-table rank tests pass; fake-engine e2e returns correctly
    ranked JSON (`TestRun_E2E_RankedJSON` — fused score, engines[] by reg order,
    engines block); exit codes 0/1/2/3 verified incl. exit-3-beats-exit-1 and
    stdout-empty-on-exit-2.
  - Binary smoke (`/tmp/mst`): `--version`→`mst dev` (0); `--help`→usage (0);
    no query→exit 2 `{"error":{"code":"usage",...}}`; no engines→exit 1 with
    `results:[]` payload + `no_results` stderr; unknown engine→exit 2 listing
    valid names; `--raw`/`--no-merge`/`-f table`→exit 2 "not yet implemented".

- **Decisions (DECISIONS.md, all 2026-07-07):** (a) ranker derives `source`
    centrally via publicsuffix; (b) unknown engine names in
    --engine/--exclude/--require → exit 2 [proposed]; (c) --no-merge/--raw deferred
    to A.2.x; (d) table→v0.2; (e) full --list-engines→A.2.2; (f) compact JSON;
    (g) `DeadlineExceeded`→`"timeout"`.
- **Blocked on:** review of decision (b) — the rest are accepted/in-plan.
- **Next:** Stage 1 — Sprint A.1.1 `brave` (keyed; httptest fixture; auto-skip
  without `BRAVE_API_KEY`; `MST_ITEST=1` smoke when keyed).
