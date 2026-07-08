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
