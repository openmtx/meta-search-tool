# DECISIONS.md

Log of implementation decisions that are **not in** `docs/SPEC.md` or that **go
against** it: ambiguities resolved, workarounds, open questions, and proposed
spec changes. `docs/SPEC.md` is the source of truth; this file is the exception
log. When a decision here is accepted, fold it into the spec and leave the entry
as a dated record. See `AGENTS.md`.

<!--
Entry template:

## YYYY-MM-DD — <short title>
- **Context:** …
- **Decision / question:** …
- **Rationale:** …
- **Status:** proposed | accepted (folded into spec §X) | rejected
-->

## 2026-07-07 — Config discovery uses MST_CONFIG_HOME, not XDG_CONFIG_HOME
- **Context:** SPEC §6 specified config discovery as
  `$XDG_CONFIG_HOME/mst/config.yaml` (fallback `~/.config/mst/config.yaml`).
  `mst` is a single self-contained CLI with no desktop-environment coupling, so
  reading a generic XDG variable is unjustified — and it clashes with the spec's
  own rule that `mst`'s control variables use the `MST_` prefix (§6).
- **Decision:** Replace the `XDG_CONFIG_HOME` override with an `mst`-owned
  `MST_CONFIG_HOME`. It points **directly at the mst config directory** (not a
  base dir), so the file is `$MST_CONFIG_HOME/config.yaml` — mirroring
  `DOCKER_CONFIG`. When unset/empty, fall back to `~/.config/mst/config.yaml`
  (unchanged). A missing file is still not an error; only an existing-but-
  unreadable/malformed file is (exit `2`).
- **Rationale:** Self-contained tool; no XDG dependency; consistent with the
  `MST_` env-var prefix the spec already mandates.
- **Status:** accepted — folded into spec §6.

## 2026-07-07 — Short flags (-e/-n/-f/-q, -v/-vv) need a non-stdlib parser
- **Context:** SPEC §4 documents short flags `-e`, `-n`, `-f`, `-q` (aliases of
  `--engine`, `--limit`, `--format`, `--query`) and `-v`/`-vv` (repeatable
  count-based verbosity). The A.0.1 implementation used Go's stdlib `flag`
  package, which cannot express short/long aliases or repeatable count flags, so
  none of these work (e.g. `mst -e brave q` → "flag provided but not defined:
  -e"). This is a deviation from §4 surfaced by the A.0.1 review.
- **Decision / question:** **Accepted — swap to `github.com/spf13/pflag`.** pflag
  supports short+long aliases (`-e`/`--engine`, `-n`/`--limit`, `-f`/`--format`)
  and repeatable count flags (`CountVarP` → `-v`/`-vv`), which stdlib `flag`
  cannot. The custom `--weight` `flag.Value` is replaced by pflag's
  `StringArrayVarP` (each occurrence appends, no comma splitting). `--help`
  returns `pflag.ErrHelp`, surfaced as `cli.ErrHelp` so `main` exits 0.
- **Rationale:** stdlib `flag` cannot represent the documented CLI surface;
  pflag is the standard, pure-Go (statically linked, no runtime dep) solution.
- **Status:** accepted — implemented; code now conforms to spec §4. §3 updated to
  name pflag. (No §4 change needed — the flags were already specified.)

## 2026-07-07 — Sprint A.0.2 scope clarifications & deferrals
- **Context:** Sprint A.0.2's DoD is the merged path: dispatcher (§7), RRF + dedup
  + sort (§9), formatters json/jsonl/text (§10), structured stderr errors + exit
  codes 0/1/2/3 (§4, §11). Several documented flags/modes are v1-scope but fall
  outside this sprint's DoD, and a few spec areas are silent. Logging them up
  front so nothing is silently dropped or silently invented.
- **(a) `source` is derived centrally by the ranker, not read from `Result.Source`.**
  §8's `Result` has a `Source` field ("registered domain (eTLD+1)") and §10/§17.13
  say `source` is "extracted via a public-suffix-aware parser". Decision: the
  ranker derives `source` from each merged entry's displayed URL via
  `golang.org/x/net/publicsuffix.EffectiveTLDPlusOne`, deterministically — it does
  NOT trust per-engine `Result.Source`. `Result.Source` stays on the struct for
  future `--raw`/native output and for engines that choose to populate it, but the
  merged JSON's `source` is always derived. Rationale: deterministic (every engine
  hitting the same canonical host yields the same source), DRY (no per-engine
  publicsuffix wiring), and matches §10's "extracted via parser" wording. This is
  a clarification of §8/§10, not a contradiction.
  **Status:** accepted (clarification; no spec change needed — consistent with §10/§17.13).
- **(b) Unknown engine names in `--engine`/`--exclude`/`--require` → exit 2.** §4
  explicitly makes an unknown name in `--weight` a usage error (exit 2) and lists
  valid names in the message. It is silent on unknown names in the other
  engine-name flags. Decision: extend the same fail-fast to `--engine`, `--exclude`,
  and `--require` — an engine name not in the registry is exit 2 with the valid
  names listed. Rationale: consistency with the documented `--weight` behavior;
  fail-fast on a typo beats silently ignoring it.
  **Status:** accepted — folded into spec §4 ("Engine selection precedence").
- **(c) `--no-merge` and `--raw` deferred from A.0.2 to A.2.x (hardening).** Both
  are v1-scope (§16) but are tier-3 debug modes and are NOT in the A.0.2 DoD.
  `--raw` additionally cannot be meaningfully exercised until real engines expose
  native payloads (Stage 1). Decision: in A.0.2 these flags surface a clear exit-2
  "not yet implemented" error (not a silent no-op) and land fully in A.2.x.
  Rationale: keeps A.0.2 to its DoD (the merged path) while never silently
  ignoring a documented flag. (`--raw --format text|table|jsonl` is already exit 2
  per §10; only the `--raw --format json` path is deferred.)
  **Status:** accepted (in-plan deferral; TASKS.md A.2.x will cover it).
- **(d) `--format table` deferred to v0.2 (§16).** Decision: in v0.1 `table`
  surfaces a clear exit-2 "planned for v0.2" error. Spec-conforming (§16 schedules
  `table` for v0.2).
  **Status:** accepted (spec-conforming).
- **(e) Full `--list-engines` output deferred to A.2.2.** A.0.2 keeps a minimal
  placeholder (prints registered engine names). The full §4 output (name/enabled/
  keyed/key_present/weight/status, respecting `--format`) lands in A.2.2
  ("hardening: --list-engines …"), which is where TASKS.md already schedules it.
  **Status:** accepted (in-plan deferral).
- **(f) JSON output is compact (not pretty-printed).** §10's `json` example is
  indented, but that is illustrative (no normative "MUST indent" statement).
  Decision: emit compact JSON for both `json` and `jsonl` (jsonl is one object per
  line by definition). Rationale: pipeline-friendly (`mst … | jq …`), smaller on
  the wire/context, consistent with jsonl, and jq/agents parse it identically. The
  field SHAPE matches §10 exactly; only whitespace differs.
  **Status:** accepted (presentation detail; spec example was illustrative).
- **(g) `context.DeadlineExceeded` is reported as the string `"timeout"`.** §11
  says a per-engine deadline firing is "reported as `timeout`", but Go's raw error
  string is "context deadline exceeded". Decision: the `engines` block `error`
  field normalizes `context.DeadlineExceeded` (and only that) to `"timeout"`; all
  other errors use `err.Error()` verbatim.
  **Status:** accepted (matches §11 wording).

## 2026-07-08 — Wikipedia OpenSearch API response type

- **Context:** The Wikipedia OpenSearch API returns a heterogeneous JSON array: `[query_string, [titles_array], [descriptions_array], [urls_array]]`. Go's `json.Unmarshal` cannot directly decode this into a single typed struct without a custom UnmarshalJSON.
- **Decision:** Use `[]json.RawMessage` for the response type. This lets us unmarshal the raw JSON once, then selectively unmarshal elements 1-3 as `[]string` (the title/description/URL arrays). Element 0 is the query string (ignored by mst).
- **Rationale:** Simpler than a custom UnmarshalJSON; zero-copy for the inner arrays; works cleanly with httptest fixtures.
- **Status:** accepted — implemented in `internal/engine/wikipedia/wikipedia.go`.

## 2026-07-08 — Centralized retry/backoff in `internal/engine/retry` (§11)

- **Context:** Sprints A.1.1 (brave) and A.1.2 (mojeek) each carried a verbatim
  copy of the same retry loop + helpers (`isRetryable`, `calcBackoff`,
  `applyRetryAfter`, `retryable`, `sleepCtx`, `jitter`). The REVIEW.md follow-up
  flagged this DRY violation (P2) and, on closer inspection during the refactor,
  the duplicated loops contained three latent bugs: (a) a **double-sleep** — the
  HTTP-error path called `retryable()` (which itself sleeps) and then `sleepCtx()`
  again, so every HTTP-error retry slept twice (~3s per exhausted-retry test
  instead of ~1.5s); (b) a **connection leak** — `defer resp.Body.Close()` inside
  the pagination loop held N bodies open until `Search()` returned; (c) a wrong
  `Retry-After` semantics — `min(Retry-After, backoff)` silently ignored the
  server's requested wait whenever it exceeded the calculated backoff.
- **Decision:** Extract a single `internal/engine/retry` package with a `Do(ctx,
  engineName, newReq)` that encapsulates the whole retry loop (§11) and returns a
  2xx response or an error. Every HTTP engine calls `retry.Do` instead of
  inlining its own loop. Three behaviors are fixed in the move:
  1. Single sleep per retry (the loop calls `Sleep` exactly once per attempt).
  2. The successful response body is closed by the caller immediately after
     parsing (no `defer` inside the loop).
  3. `ApplyRetryAfter` uses `max(Retry-After, backoff)` — never retry faster than
     the backoff, but honor a server-requested longer wait; the per-engine context
     deadline is the real cap (§11).
- **Rationale:** One correct, tested implementation shared by all engines; the
  bugs were invisible precisely because they were duplicated. Centralizing also
  makes the retry policy a single grep target for the spec-against-code check.
- **Status:** accepted — folded into spec §11 (retry policy wording +
  `internal/engine/retry` reference) and §15 (layout).

## 2026-07-08 — Paginating engines return partial results on mid-pagination failure (§7)

- **Context:** §7 stated "A failing/timed-out engine returns `(nil, error)`".
  Mojeek paginates (up to 10 pages). If page N succeeds but page N+1 exhausts
  retries, the A.1.2 code returned `(nil, error)`, discarding the pages already
  fetched (REVIEW.md P1 bug). The ranker already fuses `run.Results` regardless of
  `run.Err`, and the `engines` block reports `ok:false` with the partial `count` —
  so partial results + error flow through the pipeline correctly.
- **Decision:** A paginating engine that fails mid-way returns its accumulated
  results *plus* the error: `(allResults, lastErr)`. Single-request engines still
  return `(nil, error)` on failure. The dispatcher/ranker/format/exit-code path
  already handles non-nil error with non-empty results (ranker fuses the partial
  set; `engines` block shows `ok:false` + partial count; `--strict`/`--require`
  still surface the failure as exit 3).
- **Rationale:** Discarding fetched pages is strictly worse; the partial set is
  real, ranked data and the failure is still transparently reported.
- **Status:** accepted — folded into spec §7 (clarified the `(nil, error)`
  contract for paginating engines).

## 2026-07-08 — DDG real-world selector/URL fidelity unverified (§5 Tier C)

- **Context:** Sprint A.2.1 shipped the `ddg` HTML-scrape engine against
  synthetic httptest fixtures. The §5 access notes give explicit selectors for
  `mojeek` but are silent on DDG's exact HTML structure, and there is no
  `MST_ITEST=1` live test (§14 lists it as optional; none exists in-repo). Two
  real-world concerns surfaced in the A.2.1 review:
  1. The golden-table fixture nests `h2.result__title`/`p.result__snippet`
     *inside* `a.result__a` — the inverse of the live `html.duckduckgo.com/html/`
     markup (`h2.result__title > a.result__a`; snippet as `a.result__snippet`).
     The class-only descendant selectors (`div.result`, `a.result__a`,
     `h2.result__title`, `.result__snippet`) are robust to both shapes, so
     parsing *probably* works — but the test is near-tautological and does not
     prove live correctness.
  2. Live DDG wraps result URLs in `//duckduckgo.com/l/?uddg=<encoded>`
     redirect links. `ddg.parseResults` does no redirect unwrapping, so a
     wrapped href would flow into the ranker's canonicalizer / `source`
     derivation unchanged, yielding a `duckduckgo.com` `source` (and a canonical
     URL under `duckduckgo.com`) for every result.
- **Decision / question:** Treat `ddg` as **unvalidated against the live site**
  for v0.1. It is already off by default (§17.3) and labeled "best-effort,
  fragile" (§5), so this does not block v0.1. **Before any release that
  promotes `ddg` toward default-on or documents it for routine use: add an
  `MST_ITEST=1` live test (§14) and (a) confirm the selectors against real
  markup, (b) add `uddg=`/`/l/` redirect unwrapping if present** — or update the
  spec §5 DDG note with the confirmed selectors. No code change now.
- **Rationale:** §5 explicitly accepts DDG's fragility; shipping an opt-in
  scrape engine without a live integration test is consistent with the spec.
  Logging it here so it is tracked when DDG's reliability is next evaluated
  (§18 lists `ddg`/`mojeek` scrape viability under monitoring).
- **Status:** proposed (risk recorded; no spec change yet — re-evaluate at the
  v0.2/`table` sprint or whenever `ddg` access is next touched).

## 2026-07-08 — `-q`/`--query` flag implemented (spec deviation resolved)

- **Context:** Spec §4 flag table documents `-q, --query` as a flag alias for the
  positional query argument, but the implementation only accepted the query as
  positional args — a spec deviation flagged in the Tier 1 code review.
- **Decision:** Register `-q`/`--query` as a `StringVarP` flag in the Tier 1 flag
  set. The query can come from `-q`/`--query`, positional args, or `--stdin` — but
  only one source at a time: `--query` + positional, `--query` + `--stdin`, and
  `--stdin` + positional are all usage errors (exit 2). The `Flags.QueryFromFlag`
  field tracks whether the flag was used (vs positional), and `--help` now lists
  `--query` as the first Tier 1 flag. The `helpTiers` comment about "not a
  registered flag" is removed.
- **Rationale:** Conformance to the spec table; closes the only Tier 1 deviation.
- **Status:** accepted — folded into spec §4 (the flag table was already correct;
  the implementation now matches).

## 2026-07-08 — `--list-engines` full output + remaining v0.1-scope items

- **Context:** Sprint A.2.2 ("hardening"). DECISIONS (e) deferred the full
  `--list-engines` output (§4) to this sprint, and DECISIONS (c) assigns
  `--no-merge`/`--raw` to "A.2.x (hardening)". §16 lists `--raw`, `--no-merge`,
  and "skill-tier flag classification" as v0.1 scope. The A.2.2 DoD enumerates
  "fuzz/property tests, `--list-engines`, `--format text/table`, exit codes" but
  also says "v0.1 is taggable" — so the boundary of what blocks a v0.1 tag is
  ambiguous.
- **Decision (part 1 — `--list-engines`, implemented):**
  - Output shape per §4: each entry is `name`, `enabled` (config/flag state),
    `keyed`, `key_present` (a real bool for keyed engines, JSON `null` for
    keyless), `weight` (effective, after config + flag merge), and `status` ∈
    {`ready`,`disabled`,`missing-key`}. Renders via `format.MarshalListEngines`
    (json array / jsonl one-per-line / text aligned table; `table` → exit 2 as
    v0.2). `--list-engines` short-circuits before query resolution and exits 0.
  - State resolution is shared with `resolvePlan` via a new `computeStates`
    (resolve.go), so precedence (§6) and engine selection (--engine/--exclude)
    are identical to a normal run.
  - `--list-engines` performs **no** engine-name validation (unknown names in
    `--engine`/`--exclude` do not cause exit 2). Rationale: §4 says it "exits 0
    without searching" — it is an introspection command and should not hard-fail
    on a typo; the run-time `--engine`/`--exclude`/`--require` exit-2 validation
    still guards actual searches.
- **Question (part 2 — remaining v0.1 items, OPEN):** `--no-merge` (§10),
  `--raw` (§10), and §4's "`--help` groups flags by these tiers" are v0.1-scope
  per §16/§4 but are each non-trivial and not in A.2.2's literal DoD:
    - `--no-merge`: tractable (skip ranking; emit per-engine native-order groups
      with an `engine` field, no score/rank). Medium effort, no engine changes.
    - `--raw`: needs engines to expose their pre-`Result` native payload — an
      `Engine` interface change touching all 4 engines + their tests. Large.
    - `--help` tier grouping: custom pflag usage rendering. Small-medium.
  Should these land in A.2.2 (so v0.1 ships them), or should v0.1 tag with them
  re-deferred (keeping their current exit-2 "not yet implemented" guards for
  `--raw`/`--no-merge`) and tracked for a fast-follow?
  **Recommendation:** do `--no-merge` + `--help` tier grouping now (clean,
  in-scope, raise the bar for "taggable"), and re-defer `--raw` to its own
  sprint since it requires the Engine-interface change and is a tier-3 debug
  mode with low v0.1 ROI.
- **Status:** part 1 accepted (implemented; no spec change needed — conforms to
  §4). Part 2 proposed (awaiting decision).
