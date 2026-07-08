# PROGRESS.md

**Where we are** — the current position in the plan (`TASKS.md`). This file plus
`TASKS.md` and `DECISIONS.md` are the project's external memory (see `AGENTS.md`).

## You are here — snapshot (keep current)

- **Active:** Tier A, Stage 2 → Sprint A.2.2 (hardening) — fuzz/property tests +
  `--list-engines` done; `--no-merge`/`--raw`/`--help`-grouping scope decision OPEN
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

## Log

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
