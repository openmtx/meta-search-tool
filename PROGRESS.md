# PROGRESS.md

**Where we are** — the current position in the plan (`TASKS.md`). This file plus
`TASKS.md` and `DECISIONS.md` are the project's external memory (see `AGENTS.md`).

## You are here — snapshot (keep current)

- **Active:** Tier A, Stage 1, Sprint A.1.1 COMPLETE → next is A.1.2 (`mojeek`)
- **Last:** 2026-07-07 — Sprint A.1.1 (brave engine) complete: 3 new files,
  11 new tests, 123 total green (-race)
- **Next:** Stage 1 — Sprint A.1.2 `mojeek` (keyless HTML scrape)

## Log

<!--
Entry template:

## YYYY-MM-DD — <active Tier / Stage / Sprint>
- Done: …
- In progress: …
- Verified: … (commands run / test output)
- Blocked on: …
- Next: …
-->

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
