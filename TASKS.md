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

> ### ▶ Now working on: Tier A, Stage 1 — Sprint A.1.2 (`mojeek`)
> _Updated 2026-07-07: A.1.1 (brave) complete._

---

## Tier A — reliable general web (spec §5) ▶ current

> Stage 0 (core foundation) is **complete** — dispatch/rank/format/errors land
> real engine results correctly (112 tests green, -race). Next: Stage 1 engines
> (`brave`, `mojeek`), starting with Sprint A.1.1.

### Stage 0 — Core foundation (prerequisite for every engine)  [→ v0.1]
#### Sprint A.0.1 — Scaffold + config + engine registry
- [x] `go.mod` + module; `cmd/mst/main.go`; `internal/` layout (§15)
- [x] config discovery + load + precedence merge; enforced `version` (§6)
- [x] `Engine` interface + `Descriptor()`/`EngineInfo` + registry (§8)
- **DoD:** `go build ./...` succeeds; a unit test loads a sample config and rejects an unknown `version`; the registry enumerates engines via `Descriptor()`.
#### Sprint A.0.2 — Dispatch + ranking + output + errors
- [x] dispatcher: fan-out, `EngineRun`, concurrency cap (§7)
- [x] RRF scoring + dedup + deterministic sort (§9)
- [x] formatters: json / jsonl / text (§10)
- [x] structured stderr errors + exit codes (§4, §11)
- **DoD:** golden-table tests for RRF/dedup pass; a fake-engine end-to-end test returns correctly ranked JSON; exit codes 0/1/2/3 behave per §4; `go test ./...` green.
  - ✅ Done (2026-07-07): 112 tests green (-race). Deferred to A.2.x with
    clear exit-2 guards: `--no-merge`, `--raw` (see DECISIONS c/e); `--format
    table` → v0.2; rich `-v` logging; full `--list-engines` output.

### Stage 1 — Tier A engines  [→ v0.1]   ▶ next (Stage 0 foundation complete)
#### Sprint A.1.1 — `brave` (keyed; auto-skip without `BRAVE_API_KEY`)
- [x] `internal/engine/brave/brave.go` — Brave Search API client: HTTP GET to
  `api.search.brave.com`, parses JSON response into `[]engine.Result`
- [x] `internal/engine/brave/brave_test.go` — 10 tests: golden-table parse,
  httptest round-trip, no-key error, context cancellation, HTTP error,
  malformed response, descriptor, buildRequestURL
- [x] E2E test in `cmd/mst/main_test.go` — real brave engine through full
  pipeline with httptest server
- [x] Wire into `defaultRegistry()` in `cmd/mst/main.go`
- **DoD:** ✅ Done (2026-07-07): 123 tests green (-race). httptest fixture parses
  brave payload → `[]Result`; auto-skip without key (exit 1); E2E pipeline test
  with real engine through full dispatch→rank→format path.
#### Sprint A.1.2 — `mojeek` (keyless HTML scrape)   ▶ complete
- [x] `internal/engine/mojeek/mojeek.go` — Mojeek scrape engine: HTTP GET to
  `www.mojeek.com/search`, goquery HTML parsing, pagination (up to 10 pages),
  retry logic (§11)
- [x] `internal/engine/mojeek/mojeek_test.go` — 15 tests: golden-table parse,
  httptest round-trip, descriptor, buildRequestURL, pagination, retry (429/5xx
  succeed on retry, exhausted, context cancelled during backoff, non-retryable,
  Retry-After honored)
- [x] Wire into `defaultRegistry()` in `cmd/mst/main.go`
- **DoD:** ✅ Done: goquery fixture test parses mojeek HTML; keyless —
  `--list-engines` shows `mojeek`; 147 tests green (-race).

### Stage 2 — v0.1 completion + hardening  [→ v0.1]
#### Sprint A.2.1 — finish v0.1 engine set: `wikipedia` (Tier B) + `ddg` (Tier C, off by default)   ▶ complete
- [x] `internal/engine/wikipedia/wikipedia.go` — Wikipedia OpenSearch API client: HTTP GET to
  `en.wikipedia.org/w/api.php`, parses JSON response `[query, [titles], [descriptions], [urls]]`
  into `[]engine.Result`
- [x] `internal/engine/wikipedia/wikipedia_test.go` — 16 tests: descriptor, golden-table parse (5),
  httptest round-trip, buildRequestURL (3 cases), context cancellation, HTTP error,
  malformed response, retry (429 succeeds, exhausted, 5xx succeeds, context cancelled during backoff,
  non-retryable status)
- [x] `internal/engine/ddg/ddg.go` — DuckDuckGo HTML scrape engine: HTTP GET to
  `html.duckduckgo.com/html/?q=<q>`, goquery HTML parsing, selectors per DDG lite template:
  container `div.result`, url `a.result__a/@href`, title `h2.result__title`, snippet `.result__snippet`
- [x] `internal/engine/ddg/ddg_test.go` — 13 tests: descriptor (enabled=false), golden-table parse (3),
  httptest round-trip, buildRequestURL (2 cases), context cancellation, HTTP error,
  retry (429/5xx succeed on retry, exhausted, context cancelled during backoff, non-retryable)
- [x] Wire into `defaultRegistry()` in `cmd/mst/main.go`
- [x] E2E tests: `TestRun_Wikipedia_E2E`, `TestRun_DDG_E2E`, `TestRun_DDG_OffByDefault` in `cmd/mst/main_test.go`
- **DoD:** ✅ Done: 217 tests green (-race). Wikipedia parses JSON OpenSearch API; ddg off by default
  (explicit `--engine ddg` required); E2E pipeline tests for both engines.
#### Sprint A.2.2 — hardening: tests (§14), `--list-engines`, exit/format polish
- [x] Fuzz/property tests for canonicalization + RRF (§14): `FuzzCanonicalize`
  (idempotency/fragment-free/no-panic; 60s/4.4M-exec run clean) + property tests
  (run-reordering determinism, single-engine score monotonicity, dedup-sums,
  all-scores-positive) in `internal/rank/`
- [x] `--list-engines` full §4 output — `name`/`enabled`/`keyed`/`key_present`
  (bool or `null`)/`weight`/`status`, respecting `--format` (json/jsonl/text;
  table → exit 2). Replaces the A.0.2 placeholder. State resolution shared with
  `resolvePlan` via new `computeStates`.
- [x] `--format text` (results) verified; `--format table` → exit 2 (v0.2);
  exit codes 0/1/2/3 verified (existing suite).
- [ ] **OPEN (v0.1-tagging boundary):** `--no-merge` + `--raw` (§10; DECISIONS c
  assigns to A.2.x) and §4 `--help` tier grouping are v0.1-scope per §16 but
  not in this sprint's literal DoD and each is non-trivial (`--raw` needs an
  Engine-interface change to expose raw payloads). Decision pending — see
  DECISIONS.md (2026-07-08).
- **DoD:** `go test ./...` green incl. fuzz/property tests for canonicalization + RRF; `--list-engines`, `--format text/table`, and exit codes verified; v0.1 is taggable.
  - ✅ Partial (2026-07-08): fuzz/property + `--list-engines` + text/table/exit
    verification done; 241 tests green (-race). Remaining v0.1 items
    (`--no-merge`/`--raw`/`--help` grouping) pending scope decision.

---

## Future tiers (sketch — plan in detail when reached)
- **Tier B** Stage 1 — `arxiv`, `pubmed`, `stackexchange`, `hackernews`,
  `grokipedia`; `table` format → v0.2
- **Tier C** Stage 1 — `google`, `bing` → v0.3
- **Tier D** — `tavily`; skill affordances (`--help-json`, `--fields`,
  `--dry-run`, engine aliases) → v0.3

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
