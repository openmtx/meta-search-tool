# Meta-Search — Specification

A command-line meta-search tool that, given a single search prompt, fans out
**concurrent** queries to multiple search engines, **merges** and re-ranks the
results, and emits them as **JSON by default** so they can be consumed by an AI
agent harness.

---

## 1. Goals

- **One command, one prompt, many engines.** Issue a search once; get a single,
  unified, ranked result set.
- **AI-friendly output.** JSON is the default output format. Results are
  normalized and deduplicated so downstream LLMs/agents can ingest them directly.
- **Transparent, controllable ranking (the hero feature).** Every result carries
  an inspectable `score` and the list of engines that contributed it; weights and
  the RRF constant are tunable per call — so an AI harness can actually steer
  search quality instead of trusting a black box. See
  `competitive-research.md` (sibling doc in `docs/`).
- **Tunable engine weighting.** Each engine has a default weight; weights are
  configurable via a single user config file and per-call flags, both of which
  override the compiled-in defaults (see §6).
- **Fast.** All enabled engines are queried concurrently with a global timeout.
- **Distributable.** A single static binary with no required runtime deps.

## 2. Non-Goals (v1)

- Web UI / GUI.
- Building our own web crawler or index.
- Persistent caching of results (a simple in-process cache may be added later).
- Account/auth flows beyond API keys in env vars / config.
- Search profiles (vertical/topic search) — planned for **v1.x** (see §19), explicitly
  out of the v1 scope; v1 ships general web search only.
- **Page fetching / content extraction.** `mst` finds and ranks URLs; it does
  not fetch, render, or readability-extract page bodies, and there is no
  `--content`/`--extract` flag. A skill that needs page content composes a
  separate fetcher (mirroring the `brave-search` skill's `content.js` pattern).
  This keeps the ranking wedge sharp and the CLI surface an LLM must learn
  small (§4).

## 3. Language Choice

**Chosen: Go.** Rationale:
- First-class concurrency (goroutines + channels) maps cleanly to fan-out/fan-in.
- Excellent single-binary distribution and cross-compilation for CLI tools.
- Strong stdlib for HTTP, JSON, and context-based timeouts.

Config parsing uses `gopkg.in/yaml.v3`; HTTP is stdlib `net/http` with
`context`-based timeouts. CLI flag parsing uses `github.com/spf13/pflag` so the
short aliases (`-e`/`-n`/`-f`) and repeatable `-v`/`-vv` count flag in §4 work
(they are beyond stdlib `flag`). Rust was considered (and handles fan-out/fan-in
fine via `tokio` + `JoinSet`) but Go wins on toolchain simplicity and
iteration speed for an I/O-bound CLI.

## 4. CLI Surface

```
mst [flags] <query...>
```

Examples:
```
mst "rust async runtime comparison"
mst --engine brave,ddg --limit 20 "best espresso grinder"
mst --format text "how does riscv paging work"
mst --weight brave=2.0 --weight ddg=0.5 "noise cancelling headphones"
mst --offset 10 "rust async runtime comparison"   # next page (best-effort)
cat query.txt | mst --stdin
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-q, --query` | — | The search query (also accepted as positional args, joined by space). Specifying both `--query` and positional args is a usage error (exit 2). |
| `--stdin` | `false` | Read the query from stdin. |
| `-e, --engine` | *all enabled* | Comma-separated subset of engines to use (e.g. `brave,ddg`). |
| `--exclude` | — | Comma-separated engines to disable for this run. |
| `-n, --limit` | `10` | Max number of merged results to return. |
| `--offset` | `0` | Skip the first N merged results (best-effort pagination; see below). |
| `-f, --format` | `json` | Output format: `json` \| `jsonl` \| `text` \| `table`. |
| `--weight` | — | Per-run per-engine weight override, `ENGINE=FLOAT`; **repeatable** (e.g. `--weight brave=2.0 --weight ddg=0.5`). See "Weight semantics". |
| `--rrf-k` | `60` | RRF smoothing constant; smaller → more top-heavy ranking. |
| `--require` | — | Comma-separated engines that must run. If one cannot *start* (unknown name, disabled, or missing key) → exit `2`; if one runs but errors/times out → exit `3` (output still emitted). |
| `--strict` | `false` | Exit `3` if any engine that ran failed or timed out, even when results are returned. Off by default so `mst … \| jq …` pipelines keep working under `pipefail`; engine failures are warnings otherwise. |
| `--timeout` | `8s` | The single user-facing time budget for the run (e.g. `5s`, `500ms`). Caps the whole run; per-engine budgets derive from it. |
| `--per-engine-timeout` | *derived* | Per-engine timeout. Defaults to `min(--timeout, 6s)` — the 6s ceiling fits the full 2-retry budget (§11); set only for advanced tuning (tier 3). |
| `--no-merge` | `false` | Keep results grouped per-engine instead of merged. |
| `--raw` | `false` | Emit raw per-engine payloads without normalization. |
| `--config` | — | Path to an explicit config file (overrides discovery). |
| `--list-engines` | `false` | Print each engine's name, status, and effective weight (JSON by default); then exit `0`. See below. |
| `--verbose` | `0` | `-v` increases diagnostics (which engines ran, latency, errors). |
| `--version` | — | Print version and exit. |

**Query input resolution:** the query can come from `-q`/`--query`, from
positional args (joined by single spaces), or from `--stdin` — but only one
source at a time. Specifying **both** `--query` and positional args, **both**
`--query` and `--stdin`, or **both** `--stdin` and positional args is a usage
error → exit `2`. Supplying **none** of the three (no positional args, no
`--query`, and empty or absent stdin) is also exit `2`, as is `--stdin` with an
**empty** stream (e.g. `echo -n '' | mst --stdin`) — an empty query is a usage
error regardless of source. An empty or whitespace-only positional query (e.g.
`mst ""`) or an empty `--query` value (e.g. `mst --query ""`) is likewise
exit `2`. `--list-engines` and `--version` short-circuit before query
resolution.

**Engine selection precedence:** `--engine` sets the active set (whitelist);
`--exclude` then removes engines from that set (so `--exclude` wins on conflict).
A keyed engine missing its key is auto-skipped unless named in `--require`. An
**unknown engine name** (not in the registry) in `--engine`, `--exclude`, or
`--require` is a usage error → exit `2`, with the valid engine names listed in the
structured error message so the caller can self-correct — the same shape as the
`--weight` unknown-name rule below (e.g. `unknown engine 'bogus'; valid: brave,
mojeek, wikipedia, …`).

**Weight semantics:** `--weight ENGINE=FLOAT` overrides *only* the named
engine's weight; all other engines keep their configured/default weights (it
is **not** a full reset). A weight for an **unknown engine name** (not in the
registry) is a usage error → exit `2`; the structured error message lists the
valid engine names so the caller can self-correct (e.g. `unknown engine 'bogus'
in --weight; valid: brave, mojeek, wikipedia, …`). A weight for a known engine that is
disabled or excluded from the active set is ignored with a `verbose` notice.
Specifying `--weight` for the same engine more than once: last value wins.
Both spellings are accepted: `--weight brave=2.0` and `--weight=brave=2.0`
(the parser splits on the first `=` to separate the flag name from
`ENGINE=FLOAT`).

**`--require` ∩ `--exclude`:** if an engine appears in both `--require` and
`--exclude`, that is a config conflict → exit `2` with no output (`--exclude`
would otherwise remove from the active set the very engine `--require`
demands).

**`--list-engines` output:** lists every registered engine and exits `0`
without searching. Respects `--format` (`json` default; `jsonl` emits one
engine per line; `text`/`table` render a human-readable summary). Each entry
reports `name`, `enabled` (config/flag state), `keyed` (whether credentials are
required), `key_present` (`bool`, or `null` for keyless engines), `weight`
(effective, after config + flag merge), and `status` ∈ {`ready`, `disabled`,
`missing-key`} — so an agent can programmatically select a working engine set.
Discovery/precedence is the same as a normal run (§6).

Exit codes (highest precedence first):

- `2` — usage/config error: bad flags, malformed config, unreadable `--config`,
  or a `--require`'d engine that **cannot start** (unknown name, `enabled:
  false`, or missing key). Nothing is written to stdout; a structured JSON
  error object goes to stderr (§11).
- `3` — a `--require`'d engine ran but failed/timed out, **or** `--strict` is
  set and some engine failed. Output **is** still emitted to stdout.
- `1` — the run completed but produced zero results.
- `0` — success: ≥1 result returned and no required engine failed.

The program returns the **highest** applicable exit code — `2` over `3`, `3`
over `1`, `1` over `0` (so a `--require`'d engine that both fails *and* yields
zero results exits `3`, not `1`). On exits `1` and `3`, stdout is **always** a
complete, valid JSON payload — an empty `results: []` array for exit `1`, the
full results for exit `3`; only exit `2` writes nothing to stdout. Pipeline
consumers can therefore parse stdout unconditionally on any non-`2` exit.

**Pipeline-friendly by default:** a normal run with usable output exits `0`
even when non-required engines flake (their failures are reported in the
`engines` block and on stderr at warning level, §13), so `mst … | jq …` works
under `set -o pipefail`. Use `--strict` to make any engine failure non-zero.

**Timeout model.** The global `--timeout` is the *hard* wall-clock deadline for
the whole run; each engine also gets its own per-engine budget
(`--per-engine-timeout`). The effective per-engine budget is
`min(per_engine_timeout, remaining_global_timeout)` — a slow engine can be cut
short when the global deadline fires first. (A soft/hard timeout split was
considered and rejected as over-engineering for v1.)

### Agent ergonomics (harness-agnostic)

`mst` is designed to be driven by **any LLM harness**, through whichever path
the harness supports: **direct shell** invocation, or an
[Agent Skills](https://agentskills.io/) skill (a `SKILL.md` the agent loads on
demand). Both consume the same CLI, so the design target is simply *LLM-friendly
command construction + parseable output + recoverable errors*. To that end,
flags are **tiered** so a skill definition, tool schema, or system prompt can
say "tier-1 only":

- **Tier 1 — skill-facing (stable; the only flags a SKILL.md should document):**
  positional `--query`, `--stdin`, `-e/--engine`, `--exclude`, `-n/--limit`,
  `--offset`, `-f/--format`, `--weight`, `--list-engines`, `--version`.
- **Tier 2 — tuning (documented, discouraged for routine skill use):**
  `--rrf-k`, `--require`, `--strict`, `--timeout`.
- **Tier 3 — debug/internal (humans; an LLM should not need these):** `--raw`,
  `--no-merge`, `--per-engine-timeout`, `--config`, `--verbose`.

`--help` groups flags by these tiers.

**Canonical skill invocations:**

```
mst --format jsonl --limit 10 "<q>"               # results-only stream (cheap on context)
mst --format json  --limit 10 "<q>"               # + engines/score for reasoning
mst --engine brave --weight brave=2.0 "<q>"       # steer ranking
mst --limit 10 --offset 10 "<q>"                  # next page (best-effort)
mst --list-engines                                # introspect availability/keys
```

Use `jsonl` when the skill just needs hits (no envelope overhead); use `json`
when the agent wants `score`/`engines[]` to reason about quality (§9).

**`--offset` (pagination).** Best-effort and stateless: after merge+sort, skip
the first N results, then apply `--limit`. It is **not** a stable cursor — the
underlying engines drift between runs, so "page 2" may overlap or shift. A
stable cursor requires the deferred result cache (§18). To keep deep pages fed
with rank depth, per-engine fetch size grows with the window,
`max((offset+limit)×3, 20)` (§9). `--offset` applies only in merged mode; it is a no-op under `--no-merge` (a `-v`
notice is emitted).

## 5. Supported Engines

Engines are **plugin-style** (each implements the common interface, §8), so the
set is additive. They are grouped by **reliability and role**, because — as
real-world metasearch deployments confirm — the scraped general-web engines
(Google, DuckDuckGo, Bing, …) are the *least* dependable from a datacenter
(CAPTCHAs, anti-scrape blocks), while structured API and knowledge sources are
both more reliable and more useful for LLM grounding. Default weights and the
default-enabled set follow this tiering (§6, §17.3).

Engines are either **keyless** (work out of the box) or **keyed** (require
credentials; skipped silently if absent unless `--require`).

### Tier A — reliable general web (high default weight)

| Engine | Keyless | Auth | Notes |
|--------|:-------:|------|-------|
| `brave` | ❌ | `BRAVE_API_KEY` | Brave Search API; structured, block-resistant. **Anchor engine.** |
| `mojeek` | ✅† | none (scrape) | Independent index, scrape-tolerant; paid/beta API exists. Reliable keyless general web. |

> ⚠️ **Mojeek is HTML-scraped** — more scrape-tolerant than the Tier-C
> engines, but inherently less reliable than an API. It is the *only* keyless
> general-web engine in v1, so a keyless install has no structured-API
> general-web fallback; if its scrape breaks, the remaining keyless fallback is
> the off-by-default `ddg` (Tier C, more fragile). Monitor scrape viability (§18).

### Tier B — knowledge & reference (free APIs; keyless; high grounding value)

By role:

**Encyclopedic reference** — broad, neutral "what is X" ground truth. The two
overlap; a deployment often picks one (or weights `wikipedia` higher).

| Engine | Keyless | Auth | Notes |
|--------|:-------:|------|-------|
| `wikipedia` | ✅ | none | OpenSearch API; factual/entity ground truth. |
| `grokipedia` | ✅ | none | ⚠️ Free JSON API (`/api/full-text-search`); AI-assisted encyclopedia, neutral & factual. Upstream SearXNG flags it experimental (`inactive`). |

**Vertical specialty** — domain-specific:

| Engine | Keyless | Auth | Notes |
|--------|:-------:|------|-------|
| `arxiv` | ✅ | none | CS/physics/math papers; free API. |
| `pubmed` | ✅ | none | Biomedical literature; E-utilities API. |
| `stackexchange` | ✅ | none (key optional) | Stack Overflow + network; technical Q&A. |
| `hackernews` | ✅ | none | Tech news/discussion; Algolia API. |
| `github` | ❌ | `GITHUB_TOKEN` | Code search. **Requires a token** — `/search/code` needs auth (unauthenticated, only `/search/repositories` works at low rates). v1.x; see §18. |
| `reddit` | ⚠️ | OAuth app creds | Public consensus/discussion; semi-keyed. |

### Tier C — scraped general web (fallback; low weight; fragile)

| Engine | Keyless | Auth | Notes |
|--------|:-------:|------|-------|
| `ddg` (DuckDuckGo) | ✅ | none | HTML scrape; **best-effort, fragile** (see note). Off by default. |
| `google` | ❌ | `GOOGLE_API_KEY` + `GOOGLE_CX` | Programmable Search Engine (keyed API is fine; *scraping* Google is what gets blocked). |
| `bing` | ❌ | `BING_API_KEY` | Bing Web Search v7 (keyed API). |

### Tier D — AI-oriented (answer/summary modes; under evaluation)

| Engine | Keyless | Auth | Notes |
|--------|:-------:|------|-------|
| `tavily` | ❌ | `TAVILY_API_KEY` | AI-oriented results + answer mode (synthesized summary beyond the URL list). |

`†` Mojeek is reached by scraping in v1 (its API is paid/beta); more scrape-tolerant than the Tier-C engines. See access notes below.

> **DuckDuckGo note:** DDG's official Instant Answer API is deprecated and of
> limited coverage; `mst` instead parses the HTML results endpoint. Real-world
> metasearch deployments pin DDG at the *lowest* weight with an aggressive
> timeout — datacenter requests get heavily anti-scraped. Accordingly `ddg` is
> **off by default** (§17.3): opt in with `--engine ddg` or `ddg.enabled: true`.
> It remains a keyless fallback when no keyed engine is configured. HTML
> scraping also carries ToS/legal considerations — users are responsible for
> compliance in their jurisdiction; `mst` sends only an identifying `User-Agent`
> (§12) and bounds load via the retry policy (§11).

### Engine access notes (implementation)

- **`mojeek` (HTML scrape, keyless):** `GET https://www.mojeek.com/search?q=<q>&safe=<0|1>`.
  Paging via `s=<10×(page−1)>` — **omit `s` on page 1** (sending `s=0` triggers a
  rate-limit); max 10 pages (cap fetch at ~100). Locale via an `Accept-Language`
  header. General-result selectors (port to Go + goquery): container
  `ul.results-standard > li > a.ob`; url `a.ob/@href`; title `../h2/a`; snippet
  `..//p.s`. (Source: SearXNG `engines/mojeek.py`.)
- **`grokipedia` (JSON, keyless):** `GET https://grokipedia.com/api/full-text-search?query=<q>&limit=10&offset=<n>`.
  Returns `{results:[{slug,title,snippet}]}`; map url =
  `https://grokipedia.com/page/<slug>` (html→text on snippet). No auth — the
  cleanest integration of the new engines. Caveat: upstream SearXNG marks it
  `inactive` (experimental); validate stability before relying on it.
- **JSON-API engines** (`wikipedia`, `arxiv`, `pubmed`, `stackexchange`,
  `hackernews`, `grokipedia`) return structured results directly and are
  preferred for reliability. **Scrape engines** (`mojeek`, `ddg`) parse HTML and
  are best-effort.

**Rate limits.** Limits vary by engine and change over time; treat any numbers
as approximate and verify against each engine's docs. Structured-API engines
are reliable under moderate use; scrape engines (`mojeek`, `ddg`) are best-effort
and throttled aggressively from datacenter IPs. As a conservative default, cap
automated use at ~1 req/s per engine. `mst` makes one fan-out per run, so
rate-limit concerns are for the *harness* orchestrating many runs, not per call.

## 6. Configuration & Precedence

Precedence is **lowest → highest** (later wins):

1. **Built-in defaults** compiled into the binary (engine list, default weights,
   timeouts).
2. **Global user config:** `$MST_CONFIG_HOME/config.yaml` if `MST_CONFIG_HOME`
   is set (it points *directly* at the mst config directory, like
   `DOCKER_CONFIG`); otherwise `~/.config/mst/config.yaml` on most systems.
   `mst` owns its config location and does **not** read XDG desktop-environment
   variables.
3. **Environment variables** (e.g. `MST_ENGINES`, `BRAVE_API_KEY`, …). `mst`'s
   own control variables use the `MST_` prefix; vendor API-key variables keep
   their conventional names.
4. **CLI flags** (`--engine`, `--weight`, `--timeout`, …).

> There is **one** config file. The user config takes precedence over the
> built-in defaults, and CLI flags always win. (`--config PATH` may point at an
> explicit file instead of the discovered one.)

**Config discovery rules:** a **missing** config file is not an error — built-in
defaults are used. Only a file that *exists* but is unreadable or malformed
YAML is a config error (exit `2`). `mst` does **not** read XDG variables; its
config location is owned via `MST_CONFIG_HOME` (the same `MST_` prefix used for
its other control variables). If `MST_CONFIG_HOME` is set but empty, `mst`
treats it as unset and falls back to `~/.config/mst/config.yaml`. Unknown YAML
fields are ignored but reported as warnings at `-v`.

### Config file shape (example, YAML)

```yaml
version: 1
defaults:
  limit: 10
  timeout: 8s
  # engine_timeout omitted → defaults to min(timeout, 6s); set only for
  # advanced tuning (tier 3, §4). Mirrors the per-engine `timeout` field below.
  format: json

engines:
  # *_env fields hold the NAME of an environment variable, never the secret.
  # Keys are never stored in config files (see §12).
  # Default weights follow the §5 reliability tiers (higher = more trusted):
  #   Tier A ~1.5–2.0, Tier B ~1.0–1.5, Tier C ~0.5. Starting points; tune freely.
  brave:        { weight: 2.0, enabled: true,  api_key_env: BRAVE_API_KEY, timeout: 4s }
  wikipedia:    { weight: 1.5, enabled: true,  timeout: 3s }
  mojeek:       { weight: 1.3, enabled: true,  timeout: 4s }
  ddg:          { weight: 0.5, enabled: false, timeout: 3s }   # fragile scrape; opt-in
  google:       { weight: 1.0, enabled: false, api_key_env: GOOGLE_API_KEY, cx_env: GOOGLE_CX }
  bing:         { weight: 1.0, enabled: false, api_key_env: BING_API_KEY }
  tavily:       { weight: 1.2, enabled: false, api_key_env: TAVILY_API_KEY }
```

- **`version` is enforced.** The config must declare `version: 1` (the only
  supported value). An omitted, unknown, or mismatched version is a config error
  → exit `2` with a clear message (do not silently guess); bump it only on
  breaking config-shape changes.
- **Weights are multipliers** on an engine's normalized score (default `1.0`).
  Defaults are tiered by reliability (§5); override per engine or per call.
- **Per-engine `timeout` (optional, config only).** Tunes an engine's timeout
  by reliability — aggressive (e.g. `3s`) for engines that stall-and-block,
  generous (e.g. `6s`) for slow-but-reliable APIs. Resolution: per-engine config
  `timeout` → else `--per-engine-timeout` flag → else `min(--timeout, 6s)` (§4).
- Missing API keys → engine auto-disabled with a `verbose` notice. If the
  engine is named in `--require`, the missing key is a config error (exit `2`,
  see §4).

## 7. Execution Model

```
            ┌─────────────┐
query ────▶ │  dispatcher │ ──┬─▶ engine A ─┐
            └─────────────┘   ├─▶ engine B ─┤   ┌──────────┐   ┌──────────┐
                              └─▶ engine C ─┴──▶│ collector│──▶│  ranker  │──▶ JSON
   context (global timeout)                       └──────────┘   └──────────┘
```

- Each engine runs in its own goroutine **(Go)** carrying its own
  `context.WithTimeout`. A global context caps the whole run.
- **Concurrency cap.** Outbound engine calls are gated by an internal semaphore
  (default 8 in flight) to bound load and avoid tripping rate limits or looking
  like a burst. This is a fixed internal default in v1 — no flag, no config field
  — and is a no-op while fewer than 8 engines are enabled.
- A failing/timed-out engine returns `(nil, error)`; it does **not** abort the
  run. Errors are surfaced in the output's `engines` block. A *paginating*
  engine that succeeds on some pages but fails on a later one returns its
  accumulated results *plus* the error (partial results with error); the
  ranker still fuses the partial set and the `engines` block reports
  `ok: false` with the partial `count` and the error — strictly more useful
  than discarding the pages already fetched.
- Results from all engines flow into the **collector**, then the **ranker**.
- The merged set is sorted, truncated to `--limit`, and serialized.

## 8. Engine Interface (Go sketch)

```go
// Result is the normalized, engine-agnostic unit returned by every engine.
type Result struct {
    Title   string `json:"title"`
    URL     string `json:"url"`
    Snippet string `json:"snippet,omitempty"`
    Source  string `json:"source"`   // registered domain (eTLD+1)
}

// Options carries engine inputs. Limit is the per-engine FETCH cap derived
// in §9 (max(--limit×3, 20)) — NOT the user's --limit, which bounds only the
// final merged output.
type Options struct {
    Limit int
}

// Engine is a PURE data-fetching plugin. It knows nothing about dispatch,
// latency, or merge metadata — the dispatcher wraps its output (EngineRun).
type Engine interface {
    Name() string
    Descriptor() EngineInfo // static metadata: keyed, key env, default weight, default enabled
    Search(ctx context.Context, q string, opts Options) ([]Result, error)
}

// EngineInfo is static, compile-time metadata declared by each engine. The
// registry reads it to drive --list-engines and key/weight resolution, so the
// engine set is self-documenting rather than hardcoded per-engine in the registry.
type EngineInfo struct {
    Name          string
    Keyed         bool
    KeyEnv        string  // env var name holding the key; empty if Keyed is false
    DefaultWeight float64 // tiered by reliability (§5)
    Enabled       bool    // default enabled state (false for fragile/opt-in engines)
}

// EngineRun is assembled by the dispatcher (internal/search), NOT by engines.
// It carries the per-engine execution metadata surfaced in the output schema.
type EngineRun struct {
    Engine  string
    Results []Result   // in engine-native rank order
    Err     error
    Latency time.Duration
}

// The ranker (internal/rank, §9) consumes []EngineRun and produces its own
// aggregated result type (rank, score, contributing engines[]).
```

## 9. Merging & Ranking

`k` (default `60`, override per call with `--rrf-k`) is the standard Reciprocal
Rank Fusion smoothing constant (Cormack, Clarke & Büttcher). It dampens the
advantage of the top ranks so lower-ranked hits still contribute meaningfully:
smaller `k` → more top-heavy; larger `k` → flatter. `60` is the canonical value
used across most RRF deployments.

**Per-engine fetch size.** Each engine fetches `max((offset + limit) × 3, 20)`
results (capped by the engine's own page-size maximum), **not** just `--limit`.
RRF needs a deep native rank list per engine to fuse meaningfully — fetching
only the final window would starve the weighting signal (e.g. a weight-2.0
engine returning just 10 items would fuse over ranks 0–9 only). Growing the
fetch with `offset` (§4) keeps deep pages fed with rank depth. The window
`[offset, offset+limit)` is applied solely at step 5 (final truncation). The
multiplier is an internal constant, not user-tunable in v1. This deep fetch
applies in **all** modes, including `--raw` and `--no-merge` (raw output benefits
from the same native-rank depth).

1. **Per-engine score.** For a result at native rank `r` (0-based):
   `s = 1 / (r + k)`. Ties keep engine-native order.
2. **Weighting.** Multiply by the engine's configured weight `w`:
   `weighted = w * s`.
3. **Dedup.** Canonicalize each URL by applying these steps **in order**:
   1. lowercase scheme + host;
   2. drop default ports (`:443`/`:80`);
   3. remove the fragment;
   4. strip a trailing slash (preserving a single `/` on bare roots);
   5. strip **tracking query params** — the default conservative set is
      `utm_source`, `utm_medium`, `utm_campaign`, `utm_content`, `utm_term`,
      `gclid`, `fbclid`, `msclkid`, `mc_cid`, `mc_eid`. (`ref` is deliberately
      excluded — too many legitimate params share that name.)
   6. drop a leading `www` host label with optional trailing digits
      (`www`, `www2`, `www3`, …; so `www2.example.com/a` ≡ `example.com/a`).
   Mobile/regional host aliases (`m.`, `mobile.`, country subdomains) are
   **not** auto-collapsed, since they may serve genuinely different content.
   Percent-encoding is **not** normalized in v1 (`%20` vs `+` vs a literal
   space carry path-vs-query ambiguity), so the same page reached as
   `?q=hello%20world` and `?q=hello+world` yields two distinct canonical URLs
   and will not merge (deferred, §18). Merge entries sharing the same canonical
   URL:
   - `score` = sum of weighted scores across all contributing engines,
   - `engines` = contributing engine names, ordered by **descending weighted
     contribution**, then by engine registration name (deterministic —
     independent of goroutine scheduling, so output is reproducible run to run),
   - **snippet:** keep the one from the engine that contributed the *highest
     weighted score* for that URL; on a tie, take the engine that comes first by
     registration name (consistent with the `engines[]` ordering above). Multi-
     engine snippet concatenation is deferred (§18).
4. **Final sort** by `score` descending; ties broken by (a) number of
   contributing engines, then (b) registration order of the highest-contributing
   engine, then (c) canonical URL. All tie-breakers are deterministic, so the
   merged ordering does not depend on which goroutine happened to finish first.
5. **Output** windowed to `[offset, offset+limit)` (skip `--offset`, then take
   `--limit`), then serialized.

## 10. Output Schema

### `json` (default)

```json
{
  "schema": 1,
  "query": "rust async runtime comparison",
  "took_ms": 6109,
  "limit": 10,
  "engines": {
    "brave":     { "ok": true, "count": 10, "took_ms": 312, "error": "" },
    "ddg":       { "ok": true, "count": 9,  "took_ms": 401, "error": "" },
    "wikipedia": { "ok": false, "count": 0, "took_ms": 6000, "error": "timeout" }
  },
  "results": [
    {
      "rank": 1,
      "title": "Tokio vs async-std ...",
      "url": "https://example.com/tokio-vs-async-std",
      "source": "example.com",
      "snippet": "...",
      "score": 0.0331,
      "engines": ["brave", "ddg"]
    }
  ]
}
```

**Field semantics (all formats):**
- `schema` — output envelope version (currently `1`). Present only in the `json`
  envelope (not on `jsonl` items, which have no envelope). Bumped on breaking
  changes to the envelope/result shape; additive fields are non-breaking and do
  not bump it. Consumers should ignore unknown fields.
- `rank` — 1-indexed position in the final merged set (after sort + truncation).
- `source` — registered domain (eTLD+1) of `url` (e.g. `en.wikipedia.org` →
  `wikipedia.org`, `news.bbc.co.uk` → `bbc.co.uk`), extracted via a
  public-suffix-aware parser (see §18).
- `took_ms` — wall-clock time from dispatch start to final merged output. Because
  engines run concurrently (§7), this is approximately `max(per-engine took_ms)`
  plus merge/format overhead — it is **not** the sum of the per-engine times. In
  the example above, the run blocked ~6s on `wikipedia`'s per-engine timeout
  while `brave`/`ddg` finished in <0.5s.
- `score` — aggregate weighted RRF score (see §9). In the example above,
  `0.0331` = `1.2/(0+60)` (brave, rank 0) + `0.8/(1+60)` (ddg, rank 1).

### `jsonl` — one result object per line (no envelope) for streaming/agents.

**Not progressive.** RRF needs *all* per-engine results to compute final scores
and ordering, so `jsonl` cannot emit results incrementally: `mst` buffers every
engine, merges + ranks, then writes the final ordered list one object per line.
Wall-clock latency is therefore the same as `json`; the only win is
wire/context overhead (no array brackets, no envelope). Progressive streaming
would need a different merge strategy and is deferred (§18).
### `text` — `rank. title - url\n    snippet` (human readable).
### `table` — fixed-width columns for terminal use.

### `--no-merge` and `--raw` (non-default output modes)
- `--no-merge`: emit results grouped per-engine (each group in engine-native
  order), skipping ranking/dedup. The `results` array carries each item's
  `engine`; no `score`/`rank`.
- `--raw`: bypass normalization entirely — emit each engine's parsed-but-unmerged
  payload with engine-native fields, wrapped per engine. Intended for debugging
  and for seeing exactly what an engine returned before `mst` touches it. `--raw`
  is inherently per-engine (un-merged), so `--raw --no-merge` is accepted but a
  no-op (`--no-merge` is implied and has no additional effect).

**Composition with `--format`:**
- `--no-merge --format json`: `results` items gain an `engine` field and drop
  `score`/`rank`, grouped per engine in native order. `jsonl` emits the same
  items one per line. `text`/`table` prefix each entry with the engine name and
  drop the rank column.
- `--raw` is **JSON-only.** `--raw --format json` emits an object keyed by
  engine name, each value being that engine's parsed native payload (shape
  varies per engine — intentionally unstructured; the top-level `engines`
  metadata block is still included). `--raw` combined with `text`, `table`, or
  `jsonl` is a usage error (exit `2`).

## 11. Error Handling & Resilience

- Engine HTTP calls: up to 2 retries on 429/5xx and network/DNS errors (the
  latter treated identically to timeouts) using exponential backoff
  (base 500ms, factor 2) with ±20% jitter, honoring `Retry-After` when present.
  The retry loop is centralized in `internal/engine/retry` (§15) so every HTTP
  engine shares one correct implementation rather than duplicating the loop
  per engine. The whole sequence — initial attempt + backoff sleeps + retries
  — runs inside the engine's per-engine context (`--per-engine-timeout`); a
  deadline firing mid-retry is reported as `timeout` and the run continues.
  `Retry-After` is honored as `max(Retry-After, calculated_backoff)` — we
  never retry *faster* than the backoff strategy, but respect a
  server-requested longer wait — and the wait is capped at the remaining
  per-engine budget by the context deadline, so a server-requested long wait
  can never push an engine past its own timeout. Non-retryable status codes
  (400/401/403/404/…) return immediately without retry. The default 6s fits
  the full 2-retry sequence (~1.5s of sleeps + up to 3 round-trips) with
  margin; a tighter `--per-engine-timeout` may reduce the effective retry count
  (reported at `-v`).
- Timeouts never crash the run; they are reported per engine.
- Malformed engine payloads are skipped with a `verbose` note rather than fatal.
- Network/DNS failures treated identically to timeouts.
- **Structured errors.** Every non-zero exit emits a single JSON object on
  **stderr** (stdout untouched) so an LLM harness can parse failures
  deterministically instead of scraping free text:
  `{"error":{"code":"usage|config|no_results|engine_failure","message":"…","exit":2}}`.
  Exit `2` writes nothing to stdout (§4); exits `1`/`3` still emit their normal
  stdout payload *and* the stderr error object.

## 12. Security

- API keys **only** read from env vars or config; never logged.
- User-Agent set to `mst/<version>` — an honest, identifiable UA. Trade-off:
  it is trivially blockable (a single filter denies `mst/*`), accepted for v1
  because the structured-API engines don't care and the scrape engines
  (`mojeek`/`ddg`) are best-effort anyway. Making the UA configurable
  (`MST_USER_AGENT` / `http.user_agent`) is deferred (§18).
- Outbound requests limited to configured engine endpoints; no arbitrary URLs.
- TLS verification always on; no option to disable.

## 13. Logging & Observability

- Default log level: warnings only (to stderr, never stdout, to keep stdout
  clean for JSON piping).
- `--verbose` (`-v`, `-vv`): per-engine status, request latency, score
  breakdown.
- **The JSON envelope is the metrics feed.** The per-run `engines` block already
  carries `ok`/`count`/`took_ms`/`error` per engine — a harness logging the
  envelope accumulates per-engine success rate, latency, and failure modes over
  time. No separate `--stats` flag is planned (it would duplicate this).

## 14. Testing Strategy

- Engine clients: behind an interface; unit tests use `httptest` fixtures.
- Ranker/dedup: pure-function unit tests with golden tables.
- **Fuzz / property tests** for URL canonicalization (dedup equivalence classes)
  and RRF scoring (score monotonicity, deterministic ordering under goroutine
  reordering) — both are pure functions with clear invariants, ideal targets.
- CLI: table-driven tests exercising flag parsing and config precedence.
- Optional integration tests gated behind an env var (`MST_ITEST=1`)
  hitting live keyless engines (`ddg`, `wikipedia`).

## 15. Project Layout (Go)

```
meta-search-tool/
  docs/
    SPEC.md
  go.mod
  cmd/mst/main.go
  internal/
    config/        # discovery, load, precedence merge
    engine/        # Engine interface + registry
      retry/       # shared retry/backoff policy (§11) used by all HTTP engines
      ddg/
      wikipedia/
      brave/
      google/
      bing/
      tavily/
    search/        # dispatcher: fan-out, wrap EngineRun, collector
    rank/          # RRF scoring, dedup, sort
    cli/           # flag parsing + config binding
    format/        # output formatters: json, jsonl, text, table, raw, no-merge
  testdata/
```

## 16. Roadmap

- **v0.1:** `wikipedia` + `mojeek` + `brave` (auto-skips without a key);
  `ddg` implemented but **off by default**;
  JSON/JSONL/text output; config + flags; **`--rrf-k`**, `--require`, `--strict`,
  `--raw`, `--no-merge`, **`--offset`**; skill-tier flag classification (§4);
  structured stderr errors (§11); **opinionated default weights by reliability
  tier** (§5/§6).
- **v0.2:** keyless knowledge engines — `arxiv`, `pubmed`, `stackexchange`,
  `hackernews`, `grokipedia` (free JSON API; validate stability — upstream
  `inactive`); `table` format.
- **v0.3:** `google`, `bing`, `tavily`; skill-affordance candidates —
  `--schema`/`--help-json` (emit the flag set as JSON so a harness or tool
  wrapper can consume it), `--fields` (result projection to trim context),
  `--dry-run` (resolve config/flags/engines and print the plan without
  searching), and engine group aliases (`--engine free`/`keyed`/`all`).
- **v1.0:** stable CLI/schema; full engine set; docs; **packaged releases** —
  GitHub Releases with an OS/arch matrix (linux/macOS/Windows × amd64/arm64),
  `SHA256SUMS` for verification, and `go install` available. Code
  signing/notarization (macOS) and a Homebrew formula are v1.0 stretch goals;
  detailed install instructions live in the README.
- **v1.x (planned):** `github`, `reddit`; **search profiles / vertical search**
  (§19) — per-vertical engine subsets + query augmentation.
- **Post-v1 (deferred):** domain boosts, result cache (TTL), snippet
  concatenation option.

## 17. Resolved Decisions

1. **Language:** Go.
2. **Config format:** YAML (`gopkg.in/yaml.v3`).
3. **Default enabled engines (reliability-tiered):** the default-on set is the
   *reliable* engines — `wikipedia` and `mojeek`, plus `brave` (auto-skips if
   no key). `ddg` is **implemented
   but off by default** (fragile scrape; opt in with `--engine ddg`). All keyed
   engines (`google`, `bing`, `tavily`, …) are opt-in until enabled with keys.
   Default *weights* follow the §5 tiers (high for reliable/structured, low for
   scraped general), so a fresh install is useful for LLM grounding without
   per-call tuning.
4. **RRF constant `k`:** `60` default, tunable per call via `--rrf-k`.
5. **Snippets:** keep the single best (highest contributing weighted score);
   concatenation deferred (see §18).
6. **License:** Apache-2.0.
7. **Engine interface:** minimal — engines return `([]Result, error)`; the
   dispatcher wraps latency/error/name into `EngineRun` (see §8).
8. **Exit codes are pipeline-friendly:** a run that returns ≥1 result exits `0`
   even if non-required engines fail (failures surface in the `engines` block +
   stderr); `--strict` opts into exit `3` on any engine failure (see §4).
9. **`--require` split into two cases:** cannot-start (unknown/disabled/missing
   key) → exit `2`, no output; runs-but-fails/times-out → exit `3`, output kept.
10. **No MCP server.** The Agent Skills skill + JSON CLI subsume MCP's role for
    `mst`; a bespoke `mst mcp` would duplicate the flag/exit/output contract
    (drift risk) for a narrow, speculative audience (bash-gated or MCP-only
    harnesses). The skill is the agent surface. Revisit only on real demand; if
    ever built, generate it from `--help-json` rather than hand-maintaining a
    second surface.
11. **Reliability-tiered engine defaults.** Engine defaults (enabled set +
    weights) are tiered by dependability and LLM-grounding value (§5), informed
    by real-world metasearch tuning: structured API/knowledge sources are
    trusted and default-on; scraped general-web engines are demoted or opt-in.
12. **Concurrency cap (internal, fixed).** Outbound engine calls are gated by an
    internal semaphore (default 8 in flight) — no flag, no config field. Keeps
    the CLI surface small while bounding load; a no-op while fewer than 8 engines
    are enabled. (Resolved from §18 to a fixed internal default rather than a
    user-facing knob.)
13. **`source` eTLD+1 via `golang.org/x/net/publicsuffix`.** Registered-domain
    extraction uses the full maintained public-suffix list (not a static subset)
    so ccTLDs like `news.bbc.co.uk` resolve correctly to `bbc.co.uk`.

## 18. Open Questions / Deferred Decisions

Tracked explicitly so they don't get lost:

- **Snippet concatenation:** optional multi-engine snippet merge; deferred.
- **`--rrf-k` per-call vs config-only:** currently both (flag overrides
  config); revisit if it causes UX confusion.
- **Caching:** result cache (TTL) deferred to post-v1; decide keying strategy
  (query string? query + engine set + weights?).
- **URL canonicalization:** `www.` stripping + tracking-param removal ship in
  v1 (§9); percent-encoding normalization (`%20`/`+`/space) and mobile host-alias
  coalescing are deferred (ambiguous path-vs-query semantics; mobile hosts may
  differ in content). The tracking-param set is fixed in v1; making it
  user-configurable is deferred.
- **HTTP User-Agent:** fixed to `mst/<version>` in v1 (§12); making it
  configurable (`MST_USER_AGENT` / `http.user_agent`) and scrape-UA rotation are
  deferred.
- **Domain boosts:** deferred to post-v1; design how they interact with RRF.
- **Search profiles (§19):** **term resolved → `--profile`** (shorter, less
  jargony than `--topic`/`--vertical`); still open — whether profiles are
  built-in and/or user-defined YAML, how query augmentation is injected per
  engine, and how per-profile weights compose with global weights + RRF.
- **Skill affordances (deferred to v0.3):** `--schema`/`--help-json` (emit the
  flag set as JSON so a harness or tool wrapper can consume it), `--fields`
  (result projection to trim context), and engine group aliases
  (`free`/`keyed`/`all`, so an LLM need not hardcode names that may be
  disabled).
- **Engine access models to verify (§5):** `mojeek` (monitor scrape viability —
  it is the *only* keyless general-web engine; consider the paid/beta API path if
  scraping breaks), `reddit` (spec OAuth env vars + endpoints before implementing),
  `grokipedia` (validate stability — upstream SearXNG marks it `inactive`/
  experimental; ship `enabled: false` until confirmed). (`github` is resolved:
  `/search/code` **requires** a token — §5.) Confirm each before committing it to
  a version.
- **AI-oriented answer/summary output (§5 Tier D):** Tavily's answer mode returns
  a synthesized summary beyond the URL list — decide whether such an "answer"
  composes with RRF at all, or belongs in a separate output field. (`grokipedia`'s
  search API returns standard `{url,title,snippet}` results and composes with RRF
  normally — no special handling needed.)

## 19. Future: Search Profiles (Vertical Search)

General web search (the v0.1–v1.0 scope above) is the default and **only** mode
for now. Later, `mst` will support **search profiles** (term finalized to
`--profile`; remaining details in §18). The engine set in §5 already clusters
naturally into verticals (mirroring real-world metasearch tuning), giving
concrete profiles to ship:

- `general` → `brave`, `mojeek`, (`ddg`)
- `developer` → `github`, `stackexchange`, `hackernews`
- `academic` → `arxiv`, `pubmed`
- `community` → `reddit`, `hackernews`

**Encyclopedic reference** (`wikipedia`, `grokipedia`) is **cross-cutting** —
most profiles include it as neutral ground truth rather than pinning it to a
single vertical.

A **profile** is a named preset that, instead of using every enabled engine
equally, applies:

- **A preferred engine set** — the vertical's subset and/or per-profile weights
  (e.g. `academic` → `arxiv` + `pubmed` + `wikipedia`, weighted toward ground
  truth).
- **Query augmentation** — extra terms folded into the query sent to engines
  (e.g. `academic` → `research OR study`; or domain filters via `site:`).
- **(Possibly) per-profile domain boosts**, interacting with RRF (§18).

**General search stays the no-op default** (all enabled engines, no
augmentation). Selecting a profile is explicit, e.g. `mst --profile scholar
"transformer attention"`. Profile weights sit at the **config layer** in the
precedence chain (§6): an explicit per-call `--weight ENGINE=FLOAT` always
overrides the profile's weight for that engine. True to `mst`'s transparency principle, the output
envelope will report the **effective query** (post-augmentation) plus which
engines/weights the profile selected, so the result is auditable.

> **Scope note:** details — the keyword-injection strategy, per-profile weight
> files, and whether profiles ship built-in or are user-defined — are deferred
> (§18). This section exists to keep the feature on the roadmap and to ensure v1
> isn't designed in a way that blocks it (e.g. the query pipeline and output
> schema must accommodate an augmented query + per-call engine/weight
> selection).
