---
name: web-search
description: >-
  Search the web with `mst`, a single-binary meta-search CLI. Fans out to
  multiple engines concurrently, merges results with tunable RRF ranking,
  emits JSON. Use for current web info, facts, docs, or URLs — not for
  reading page bodies (call a fetch tool on returned URLs).
license: Apache-2.0
compatibility: Auto-installs a pinned `mst` to ~/.cache/agent-skills/web-search/bin/ (not PATH); no key required by default.
metadata:
  version: "1.0.0"
  requires:
    mst: ">=0.3,<1.0"
---

# web-search (mst)

## Setup

`mst` is **not on PATH** — this skill keeps its own isolated copy at
`~/.cache/agent-skills/web-search/bin/mst`, so it never clashes with another
skill's `mst`. Install or verify it (downloads ~11 MB, SHA256-verified; pins an
exact tested version — currently v0.3.2):

```bash
curl -fsSL https://raw.githubusercontent.com/openmtx/meta-search-tool/master/skills/web-search/scripts/setup.sh | bash
```

It prints the binary path and is a fast no-op if the pinned version is already
cached. Then set a variable for the examples below:

```bash
MST=~/.cache/agent-skills/web-search/bin/mst
"$MST" --version       # e.g. mst v0.3.2
"$MST" --list-engines  # engines, status, effective weights
```

A keyed engine without its env var (e.g. `BRAVE_API_KEY`) is auto-skipped,
not an error — unless named in `--require`.

## Usage

Examples assume `MST=~/.cache/agent-skills/web-search/bin/mst` (set in Setup).

**Results only (cheap on context):**
```bash
"$MST" --format jsonl --limit 10 "<query>"
```

**Full envelope (per-engine status + score):**
```bash
"$MST" --format json --limit 10 "<query>"
```

**Steer ranking per query:**
```bash
"$MST" --engine brave,wikipedia --weight brave=2.0 "<query>"
```

**Pagination (best-effort, stateless):**
```bash
"$MST" --limit 10 --offset 10 "<query>"
```

**Read query from stdin:**
```bash
printf '%s' "<long query>" | "$MST" --stdin --format jsonl
```

Use `jsonl` when you just need hits; use `json` when you want `score`/`engines[]`
to reason about quality.

## Engines

No `--engine` flag runs the **default-on set** (general-web only): `ddg`, `google`, and `brave` (skips without `BRAVE_API_KEY`). Academic,
reference, news, and q&a engines are NOT in the default run — select them with
an explicit category alias (`--engine academic`, `--engine reference`, `--engine
q&a`, `--engine all`). `--engine` composes with engine names; duplicates removed:

| `--engine` | Engines |
|------------|---------|
| `general` | brave, ddg, mojeek, google, bing, tavily |
| `reference` | wikipedia, grokipedia |
| `academic` | arxiv, pubmed |
| `news` | hackernews |
| `'q&a'` | stackexchange *(quote it; `&` is a shell operator)* |
| `free` | all keyless engines |
| `all` | every registered engine |

Of the `general` engines, only `ddg`, `google`, and `brave` are on by default;
`mojeek`, `bing`, and `tavily` are opt-in via config or `-e` (all other
categories are opt-in too — see above). Keyed (auto-skip without their env var):
`brave`, `bing`, `tavily`. Inspect live state with `"$MST" --list-engines`.

```bash
"$MST" --engine academic "vision transformer 2025"
"$MST" --engine academic,brave "transformer attention"   # compose with general web
"$MST" --engine free "best espresso grinder"
```

## Errors

| Exit | Meaning | Action |
|------|---------|--------|
| 0 | success | — |
| 1 | zero results | broaden query, add engines, raise `--limit` |
| 2 | bad flags / config error / missing key for a `--require`'d engine | fix command |
| 3 | a `--require`'d engine ran but failed, or `--strict` and an engine failed | stdout still has results; `--exclude` the flaky one |

Error details are a JSON object on stderr (`{"error":{...}}`). On exits 1 and 3,
stdout is still a complete, valid JSON payload; only exit 2 writes nothing to
stdout — parse stdout unconditionally on any non-2 exit.

## Flags

**Tier 1 — routine use (the only flags you normally need):**
`<query>` (positional), `--stdin`, `-e/--engine`, `--exclude`, `-n/--limit`,
`--offset`, `-f/--format`, `--weight`, `--list-engines`, `--version`.

**Tier 2 — tuning (use only when you need them):**
`--rrf-k` (ranking smoothing), `--require` (engine must run or exit), `--strict`
(exit 3 on any engine failure), `--timeout` (run budget).

**Tier 3 — debug/internal (avoid; humans only):**
`--raw`, `--no-merge`, `--per-engine-timeout`, `--config`, `--verbose`.
