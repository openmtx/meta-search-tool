---
name: web-search
description: Search the web with `mst`, a single-binary meta-search CLI that runs one query across many search engines concurrently and returns one merged, re-ranked JSON result set with transparent scores. Use whenever the task needs current web information, facts, documentation lookups, or a list of relevant URLs — especially when ranking quality matters or you want to steer which engines to trust per query.
license: Apache-2.0
compatibility: Requires the `mst` binary on PATH (v0.1+). Optional engine API keys (e.g. BRAVE_API_KEY, GOOGLE_API_KEY) enable more engines; keyless engines (Wikipedia, Mojeek) work with zero setup.
---

# Web Search (via `mst`)

`mst` (meta-search tool) queries many engines at once, dedups and re-ranks the
results with Reciprocal Rank Fusion (RRF), and prints JSON. This skill wraps its
**tier-1** flag set — the small, stable subset designed for agent use.

**Scope:** `mst` returns **URLs and snippets only.** It does not fetch or read
page bodies. If you need a page's content, call a separate fetch/readability
tool on a URL this skill returns.

## Setup

### Option A — already installed

Ensure `mst` is on `PATH`:

```bash
mst --version
mst --list-engines
```

Each entry reports `status` ∈ {`ready`, `disabled`, `missing-key`}, plus
`enabled`, `keyed`, `key_present`, and effective `weight`.

### Option B — download the prebuilt binary (recommended for skills)

If `mst` is not on `PATH`, the setup script downloads the right binary for the
current platform from GitHub Releases and places it in `~/.local/share/mst/bin/`:

```bash
source <(curl -sL https://raw.githubusercontent.com/openmtx/meta-search-tool/master/skills/web-search/setup.sh)
```

This adds the binary to PATH for the current shell. Subsequent runs use the
cached binary; re-run the script to update.

> **If the download fails:** the environment doesn't have `mst`, and no fallback
> to `curl`-based single-engine search is attempted. Falling back to calling one
> engine directly (which one? SearXNG public instance? Brave API?) would lose
> the core value of `mst` — concurrent multi-engine search with RRF fusion, dedup,
> and transparent scoring. A degraded single-engine fallback would silently
> produce worse results without the caller knowing. Clear failure is better:
> the skill reports the error and tells the caller to install `mst` manually:
>
> ```bash
> go install github.com/openmtx/meta-search-tool/cmd/mst@latest
> ```

## Search

Results-only output — **prefer this** (no envelope overhead, cheapest on context).
Emitted only after all engines finish and results are ranked — **not** a
progressive stream:

```bash
mst --format jsonl --limit 10 "<query>"
```

Full envelope with per-engine status + scores — use when you want to reason
about *why* results ranked where they did:

```bash
mst --format json --limit 10 "<query>"
```

### Output shape

`jsonl` — one result object per line, no envelope:

```json
{"rank":1,"title":"...","url":"https://example.com/a","source":"example.com","snippet":"...","score":0.0331,"engines":["brave","ddg"]}
```

`json` — envelope with an `engines` status block and a `results[]` array:

```json
{
  "schema": 1,
  "query": "...",
  "took_ms": 6109,
  "limit": 10,
  "engines": { "brave": {"ok":true,"count":10,"took_ms":312,"error":""} },
  "results": [
    { "rank":1,"title":"...","url":"https://example.com/a","source":"example.com","snippet":"...","score":0.0331,"engines":["brave","ddg"] }
  ]
}
```

- `score` — higher is better; a weighted RRF score summed across engines.
- `engines[]` — which engines contributed this URL. More engines = stronger
  corroboration.

## Steering ranking (the key capability)

Trust an engine more or less for *this* query (repeatable):

```bash
mst --engine brave,ddg --weight brave=2.0 --weight ddg=0.5 "<query>"
```

Restrict to a subset, or drop a flaky engine:

```bash
mst --engine brave,wikipedia "<query>"
mst --exclude ddg "<query>"
```

## Pagination

`--offset` skips merged results. It is **best-effort**: engines drift between
runs, so "page 2" may overlap page 1 — re-issue the same query to advance:

```bash
mst --limit 10 --offset 10 "<query>"   # results 11–20
```

## Reading a query from stdin

Pipe a long query (e.g. assembled from context) via stdin:

```bash
printf '%s' "<long query>" | mst --stdin --format jsonl
```

## Errors and recovery

`mst` exits non-zero with a **JSON object on stderr**. On exits `1`/`3`, stdout
is still valid JSON; only exit `2` writes nothing to stdout. Parse stderr to
recover:

| Exit | Meaning | Recover by |
|------|---------|------------|
| `0`  | success | — |
| `1`  | zero results | broaden the query, add engines, or raise `--limit` |
| `2`  | usage/config error (bad flag, or a `--require`'d key missing) | fix the command; nothing reached stdout |
| `3`  | an engine ran but failed, or `--strict` was set | results are still on stdout; usually safe to ignore unless you `--require`'d the failing engine |

Example stderr: `{"error":{"code":"engine_failure","message":"ddg: timeout","exit":3}}`

A flaky keyless engine (e.g. the DuckDuckGo HTML scrape) failing is **normal**
and only shows up in the `engines` block — it does not stop the run. Use
`--exclude` to silence a chronically broken engine.

## Decision guide

- Just need hits? → `--format jsonl`.
- Choosing between answers or citing the strongest source? → `--format json`,
  read `score` + `engines[]`, and `--weight` the engine you trust.
- Need more results? → `--offset` to page, or raise `--limit`.
- An engine keeps failing? → `--exclude` it.

## Flags this skill uses (tier-1 only)

`<query>` (positional), `--stdin`, `-e/--engine`, `--exclude`, `-n/--limit`,
`--offset`, `-f/--format`, `--weight`, `--list-engines`, `--version`. Avoid the
debug/tuning flags (`--raw`, `--no-merge`, `--rrf-k`, `--require`, `--strict`,
`--timeout`, `--per-engine-timeout`, `--config`, `--verbose`) unless you are
diagnosing `mst` itself.
