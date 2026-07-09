# TASKS.md

**What to do** — the plan, organized as **Tier → Stage → Sprint** (see
`AGENTS.md`). Tiers follow the engine-reliability tiers in `docs/SPEC.md` §5
(A → B → C → D) and track the roadmap (§16); stages are phases within a tier;
sprints are verifiable units of work sized to one session. Check items off as
they land. This file plus `PROGRESS.md` (where we are) and `DECISIONS.md`
(exceptions) are the project's external memory — keep them current so any stopped
session resumes by reading them.

> **Follow the design at `docs/SPEC.md`.** It is authoritative — implement
> exactly what it says. If anything is unclear, silent, or you'd do it
> differently, log it in `DECISIONS.md` and **ask** before proceeding; don't
> invent or silently deviate. Section pointers below (§N) refer to that doc.

<!--
Structure template:

## Tier X — <name> (spec §5)
### Stage N — <name>   [→ target version]
#### Sprint X.N.M — <goal>
- [ ] task
- **DoD:** <verifiable completion criterion>
-->

## Tiers (engine-reliability — docs/SPEC.md §5)

Work proceeds tier by tier (A → B → C → D), tracking the roadmap (§16):

- **Tier A** — reliable general web: `brave`, `mojeek`, plus the **core
  foundation** (dispatch, rank, config, CLI, format) → **v0.1**
- **Tier B** — knowledge & reference: `wikipedia` → v0.1; then `arxiv`, `pubmed`,
  `stackexchange`, `hackernews`, `grokipedia` → v0.2
- **Tier C** — scraped general web: `ddg` (off by default) → v0.1; then `google`,
  `bing` → v0.3
- **Tier D** — AI-oriented: `tavily` → v0.3

> **v0.1 ships when** Tier A (foundation + engines) + `wikipedia` (Tier B) +
> `ddg` (Tier C, off by default) + hardening are done (§16).

> ### ▶ Now working on: Tier D, Stage 1 — Sprint D.1.2 (`include_answer`, `search_depth`)
> _Updated 2026-07-09: All tiers complete; Stage 1 finished; next: Stage 2 hardening or release tagging._

### Stage 0 — Core foundation (prerequisite for every engine)  [→ v0.1]
#### Sprint A.0.1 — Scaffold + config + engine registry
- [x] `go.mod` + module; `cmd/mst/main.go`; `internal/` layout (§15)
- [x] config discovery + load + precedence merge; enforced `version` (§6)
- [x] `Engine` interface + `Descriptor()`/`EngineInfo` + registry (§8)
#### Sprint A.0.2 — Dispatch + ranking + output + errors
- [x] dispatcher: fan-out, `EngineRun`, concurrency cap (§7)
- [x] RRF scoring + dedup + deterministic sort (§9)
- [x] formatters: json / jsonl / text (§10)
- [x] structured stderr errors + exit codes (§4, §11)

---

## ✅ Tier A — reliable general web — done (v0.1, 2026-07-08)
Shipped: core foundation + `brave` + `mojeek`; plus `wikipedia` (Tier B) and
`ddg` (Tier C, off by default). Full hardening: fuzz/property tests, `--list-engines`,
`--no-merge`, `--raw`, `--help` tier grouping. 262 tests green (-race).

---

## Tier B — knowledge & reference (spec §5) ▶ next
### Stage 1 — Knowledge engines  [→ v0.2]
#### Sprint B.1.1 — `arxiv` (keyless Atom XML API)   ▶ complete
- [x] `internal/engine/arxiv/arxiv.go` — arXiv API client: HTTP GET to
  `export.arxiv.org/api/query`, parses Atom XML into `[]engine.Result`
- [x] `internal/engine/arxiv/arxiv_test.go` — 14 tests: descriptor, golden-table
  parse (3), httptest round-trip, buildRequestURL (3 cases), context cancellation,
  HTTP error, malformed response, retry (429 succeeds on retry, exhausted)
- [x] Wire into `defaultRegistry()` in `cmd/mst/main.go`
- **DoD:** ✅ Done: goquery fixture test parses arXiv Atom XML; keyless —
  `--list-engines` shows `arxiv`; 13 packages green (-race), +14 new tests.

#### Sprint B.1.2 — `pubmed` (keyless E-utilities API)   ▶ complete
- [x] `internal/engine/pubmed/pubmed.go` — PubMed E-utilities API client: HTTP GET to
  `eutils.ncbi.nlm.nih.gov/entrez/eutils/esearch.fcgi` + `esummary.fcgi`,
  parses XML into `[]engine.Result`
- [x] `internal/engine/pubmed/pubmed_test.go` — descriptor, golden-table parse (5),
  httptest round-trip, buildRequestURL (3 cases), context cancellation, HTTP error,
  malformed response, esearch-empty, retry (429 succeeds, exhausted)
- [x] Wire into `defaultRegistry()` in `cmd/mst/main.go`
- **DoD:** ✅ Done: pubmed engine implements the NCBI E-utilities esearch+esummary
  XML API; 17 tests green (-race); full suite 303 tests green (-race);
  `--list-engines` shows `pubmed` with status `ready`.

#### Sprint B.1.3 — `stackexchange` (keyless API)   ▶ complete
- [x] `internal/engine/stackexchange/stackexchange.go` — Stack Exchange Search API
  client: HTTP GET to `api.stackexchange.com/2/search/advanced`, parses JSON into
  `[]engine.Result`
- [x] `internal/engine/stackexchange/stackexchange_test.go` — descriptor, golden-table
  parse (5), httptest round-trip, buildRequestURL (3 cases + path check),
  context cancellation, HTTP error, malformed response, API error, retry (429
  succeeds, exhausted)
- [x] Wire into `defaultRegistry()`
- **DoD:** ✅ Done: stackexchange engine implements the Stack Exchange Search JSON
  API; strips HTML tags from body content; 16 tests green (-race); full suite
  320 tests green (-race); `--list-engines` shows `stackexchange` with status
  `ready`. Weight 1.0 (Tier B knowledge, lower than arxiv/pubmed for structured
  reference).

#### Sprint B.1.4 — `hackernews` (keyless Algolia API)   ▶ complete
- [x] `internal/engine/hackernews/hackernews.go` — Hacker News Algolia Search API
  client: HTTP GET to `hn.algolia.com/api/v1/search`, parses JSON into
  `[]engine.Result`
- [x] `internal/engine/hackernews/hackernews_test.go` — descriptor, golden-table
  parse (6), httptest round-trip, buildRequestURL (3 cases), context cancellation,
  HTTP error, malformed response, retry (429 succeeds, exhausted)
- [x] Wire into `defaultRegistry()`
- **DoD:** ✅ Done: hackernews engine implements the HN Algolia Search JSON API;
  constructs HN URLs from objectID when direct URL missing; includes date/points
  in snippet; 17 tests green (-race); full suite 337 tests green (-race);
  `--list-engines` shows `hackernews` with status `ready`. Weight 0.8 (Tier B
  knowledge, community/discussion source).
- **Note:** Uses `hn.algolia.com/api/v1/search` with `tags=story` filter for
  HN story search (not comments or jobs).

#### ✅ Sprint B.1.5 — `grokipedia` (keyless JSON API)   ▶ complete
- [x] `internal/engine/grokipedia/grokipedia.go` — Grokipedia full-text search
  API client: GET `grokipedia.com/api/full-text-search?query=&limit=`, parses
  JSON `{results:[{slug,title,snippet}]}` into `[]engine.Result`; URL =
  `grokipedia.com/page/<slug>`; HTML stripped from snippet (§5).
- [x] `internal/engine/grokipedia/grokipedia_test.go` — descriptor, golden-table
  parse (6: normal/empty/missing-slug/missing-title/html-snippet/malformed),
  httptest round-trip, buildRequestURL (3 cases), context cancellation, HTTP
  error, malformed response, SearchWithRaw, retry (429 succeeds, exhausted),
  + gated `TestSearch_Live` (MST_ITEST=1).
- [x] Wire into `defaultRegistry()`.
- **DoD:** ✅ Done (after 2026-07-09 review fixes): correct `/api/full-text-search`
  path, html→text snippet stripping (§5), live test **green** (10 real results).
  Ships `enabled: false` (SearXNG marks experimental/inactive); full suite **360
  tests green** (-race) + 2 live-test skips; `--list-engines` shows `grokipedia`
  with status `disabled`, weight 1.0.
- **Note:** upstream SearXNG marks grokipedia `inactive` (experimental); ship
  with `enabled: false` until stability confirmed. Review found & fixed a path
  bug (dropped `/api` segment, masked by its own tests) + missing html→text —
  see DECISIONS 2026-07-09.

#### Sprint B.1.6 — `table` format (§10) + E2E integration tests   ▶ complete
- [x] `internal/format/table.go` — fixed-width column rendering for terminal use
- [x] `internal/format/format_test.go` + `table_test.go` — table rendering tests (wide/narrow titles,
  truncated URLs, snippet wrapping, unicode)
- [x] `MST_ITEST=1` live integration test gate (§14) for keyless engines
- [x] Wire `table` into `validateFormat` + `Marshal`/`MarshalUnmerged` (remove exit-2 guard)
- **DoD:** ✅ Done: `go test -race ./...` → 391 tests green; `--format table` renders results in a
  terminal-friendly table with RANK/TITLE/URL/SNIPPET columns; --no-merge table with ENGINE column;
  wide titles/snippets truncated with "…"; E2E pipeline tests for merged + unmerged paths.

### Stage 2 — Tier B hardening  [→ v0.2]
#### ✅ Sprint B.2.1 — `table` format polish + `MST_ITEST=1` live tests for all Tier B engines   ▶ complete
- [x] Live integration tests: arxiv, pubmed, stackexchange, hackernews, grokipedia (all 5 already had MST_ITEST=1; verified green)
- [x] `table` format polish: URL truncation (scheme+host preserved), maxURLWidth constant, truncateURL helper
- [x] `table` format: truncateURL unit tests + integration tests for MarshalTable/MarshalTableUnmerged
- [x] Update DECISIONS with grokipedia stability assessment

### Stage 3 — Engine categories & group aliases  [→ v0.3]
#### ✅ Sprint B.3.1 — Engine categories & group aliases   ▶ complete
- [x] Add `Category` field to `EngineInfo` in `internal/engine/engine.go`
- [x] Add `Registry.ExpandNames` for category/alias resolution
- [x] Assign categories to every engine (9 engines: brave/mojeek/ddg → general,
      wikipedia/grokipedia → reference, arxiv/pubmed → academic,
      hackernews → news, stackexchange → q&a)
- [x] `--engine <category>` expands to all engines with that category
- [x] Built-in aliases: `--engine free` (keyless), `--engine keyed` (keyed),
      `--engine all` (all engines)
- [x] `--list-engines` shows CATEGORY column in text format, `category` in JSON
- [x] Mixing aliases with engine names: strict-whitelist semantics, dedup'd
- [x] Update spec §4, §8, §5, §16
- [x] Revisit arxiv AND fix: categories make OR-flooding unreachable for default
      (general) queries since arxiv won't run under `general` category — the
      AND fix was already reverted in favor of categories.
- **DoD:** ✅ 390 tests green (-race); `--engine academic "transformer"` runs
  only arxiv/pubmed; `--engine free` selects all keyless; `--engine all` selects
  all (missing-key still auto-skip); `--list-engines` shows categories;
  unknown category names exit 2 (rejected as unknown engine names).

---

## Tier C — scraped general web (spec §5)
### Stage 1 — Scraped engines  [→ v0.3]

#### ✅ `ddg` redesign — SearXNG mechanism (2026-07-09, interlude)
`ddg` was first shipped in A.2.1 with guessed selectors and an unlogged §12
violation (spoofed browser UA). Redesigned to actually work by following
SearXNG (`docs/DDG-research.md`): form **POST** to `html.duckduckgo.com/html/`,
static browser UA + `Sec-Fetch-*`/`Referer` headers, confirmed selectors
(`#links > div[class*=web-result]`, `h2 a`, `a.result__snippet`), CAPTCHA
detection (`ErrCaptcha`), ≥500-char query guard, `uddg=` unwrap. §12 scrape-UA
carve-out folded into spec. **`MST_ITEST=1` live test green** (10 real
results); full suite 341 tests green (-race). Single page only — see follow-up.

#### ✅ Sprint C.1.0 — `ddg` `vqd` multi-page   ▶ complete
- [x] Extract `vqd` from page-1 (`<input name="vqd">`), cache per Search call
- [x] Pages 2+ POST with `s`/`dc`/`vqd`/`api=d.js`/`o=json`/`v=l` (SearXNG
      offset formula: offset = 10 + (pageno-2)*15)
- [x] Partial-results contract (§7): if page N fails, return accumulated
      results with error
- [x] 3 new tests: multi-page httptest, partial-failure CAPTCHA, no-vQD stop
- [x] Live test verifies ≥30 results (was ~10 from single page)
- **DoD:** `MST_ITEST=1` live test returns **30 results** (FetchLimit for
  `--limit 10`); 401 tests green (-race) + 7 skips; `ngine ddg "nvidia news"`
  returns 24 real NVIDIA news results (up from ~10).

#### ✅ Sprint C.1.1 — `google` (hybrid API + scrape)   ▶ complete
- [x] `internal/engine/google/google.go` — hybrid mode: if GOOGLE_API_KEY +
      GOOGLE_CX are set → official Custom Search JSON API; otherwise → HTML
      scrape fallback (SearXNG mechanism: GET google.com/search, /url?q= URL
      unwrap, CAPTCHA/sorry detection, CONSENT cookie bypass).
- [x] `internal/engine/google/google_test.go` — 39 tests: descriptor, API golden
      parse (3), API httptest, buildAPIURL (3), API error paths (5), API retry
      (6), API SearchWithRaw, scrape buildScrapeURL (3), isGoogleSorry (4),
      unwrapGoogleURL (3), parseScrapeResults (3), scrape httptest, scrape
      CAPTCHA, scrape multi-page, scrape context cancelled.
- [x] Wire into `defaultRegistry()`
- **DoD:** `go test -race ./...` green; `--list-engines` shows `google` as
  `keyless, disabled, weight=1.0`. 447 tests green (-race).

#### ✅ Sprint C.1.2 — `bing` (keyed API)   ▶ complete
- [x] `internal/engine/bing/bing.go` — Bing Web Search v7 API client
- [x] `internal/engine/bing/bing_test.go` — 20 tests: descriptor, golden-table parse (5), httptest round-trip, buildRequestURL (5 cases), error paths (4), retry (6), SearchWithRaw (2)
- [x] Wire into `defaultRegistry()`
- **DoD:** ✅ Done: 20 bing tests green (-race); full suite 381 tests green (-race); `--list-engines` shows `bing` with status `disabled, keyed, weight=1.0, category=general`. Weight 1.0 (Tier C, keyed API).

---

## ✅ Tier D — AI-oriented — done (v0.3)
Shipped: `tavily` with `include_answer` and `search_depth` options (Sprint D.1.2).

### Stage 1 — AI answer engines  [→ v0.3]
#### ✅ Sprint D.1.1 — `tavily` (keyed API)   ▶ complete
- [x] `internal/engine/tavily/tavily.go` — Tavily Search API client: POST to
      `api.tavily.com/search`, parses JSON `{results:[{url,title,content}]}` into
      `[]engine.Result`; limit capped at 50 (free tier), floored at 1; html→text
      snippet stripping (§5).
- [x] `internal/engine/tavily/tavily_test.go` — descriptor, golden-table parse
      (5: normal/empty/skip-empty-URL/api-error/html-content), httptest round-trip,
      buildRequestURL (5 cases), context cancellation, HTTP error, malformed
      response, retry (429 succeeds on retry, exhausted, 5xx succeeds, context
      cancelled during backoff, non-retryable 401, Retry-After honored),
      SearchWithRaw (2), limitCap (6 cases), + gated `TestSearch_Live`
      (MST_ITEST=1).
- [x] Wire into `defaultRegistry()`.
- **DoD:** ✅ Done: 22 tavily tests green (-race); full suite **403 tests green**
  (-race) + 8 skips; `--list-engines` shows `tavily` with status `disabled, keyed,
  weight=1.2, category=general`. Weight 1.2 (Tier D, AI-oriented).

#### ✅ Sprint D.1.2 — tavily follow-ups: `include_answer`, `search_depth`, answer field   ▶ complete
- [x] `include_answer` option passed to Tavily API (config: `engines.tavily.include_answer`, default false)
- [x] `search_depth` option passed to Tavily API (config: `engines.tavily.search_depth`, default "basic")
- [x] `Answer` field parsed from Tavily response and surfaced as top-level envelope field (not composed with RRF)
- [x] `GetAnswerFromRaw` helper to extract answer from raw payload
- [x] Per-engine options plumbing: `engine.Options.IncludeAnswer` + `SearchDepth`, config → resolve → dispatch → engine
- [x] 13 new tavily tests: include_answer true/default, search_depth advanced/empty, answer in response, answer when not requested, GetAnswerFromRaw (5 cases)
- [x] `config.EngineConfig.IncludeAnswer` + `SearchDepth` YAML fields
- [x] Decision logged in DECISIONS.md: answer is a separate envelope field, NOT composed with RRF
- **DoD:** ✅ Done: 35 tavily tests green (-race); full suite **508+ tests green**
  (-race); `--format json` output includes `"answer"` field when tavily returns one;
  config YAML supports `include_answer` and `search_depth` under `engines.tavily`.

---

## When a tier is done — compress it

When a tier is complete, **collapse its detailed section into a single summary
line** (engines shipped + version + date) and leave only that here. The per-task
detail is already captured in `PROGRESS.md` and git history, so `TASKS.md` should
stay focused on the **current and remaining** tiers — not grow unbounded. Move
the "▶ Now working on" marker to the next tier at the same time.

<!--
Compressed-tier template (replaces the detailed section):

## ✅ Tier A — reliable general web — done (v0.1, YYYY-MM-DD)
Shipped: core foundation + `brave` + `mojeek`; plus `wikipedia` (Tier B) and
`ddg` (Tier C, off by default) to complete v0.1.
-->
