---
name: web-search
description: >-
  Search the web with `mst`, a single-binary meta-search CLI. Fans out to
  multiple engines concurrently, merges results with tunable RRF ranking,
  emits JSON. Use for current web info, facts, docs, or URLs — not for
  reading page bodies (call a fetch tool on returned URLs).
license: Apache-2.0
compatibility: Requires `mst` on PATH. Get it from GitHub Releases (https://github.com/openmtx/meta-search-tool/releases) or `go install github.com/openmtx/meta-search-tool/cmd/mst@latest` (needs Go).
---

# web-search (mst)

## Setup

`mst` must be on PATH. The skill does not install it — obtain it yourself:

- **Prebuilt binary (no Go needed):** download from
  [GitHub Releases](https://github.com/openmtx/meta-search-tool/releases) —
  pick the archive for your OS/arch (`linux`/`darwin`/`windows` ×
  `amd64`/`arm64`), extract, and put `mst` on your PATH.
- **From source (needs Go):**
  `go install github.com/openmtx/meta-search-tool/cmd/mst@latest`

Verify:
```bash
mst --version        # installed and on PATH
mst --list-engines   # engines, status, effective weights
```

A keyed engine without its env var (e.g. `BRAVE_API_KEY`) is auto-skipped,
not an error — unless named in `--require`.

## Usage

**Results only (cheap on context):**
```bash
mst --format jsonl --limit 10 "<query>"
```

**Full envelope (per-engine status + score):**
```bash
mst --format json --limit 10 "<query>"
```

**Steer ranking per query:**
```bash
mst --engine brave,wikipedia --weight brave=2.0 "<query>"
```

**Pagination (best-effort, stateless):**
```bash
mst --limit 10 --offset 10 "<query>"
```

**Read query from stdin:**
```bash
printf '%s' "<long query>" | mst --stdin --format jsonl
```

Use `jsonl` when you just need hits; use `json` when you want `score`/`engines[]`
to reason about quality.

## Engines

No `--engine` flag runs the **default-on set**: `brave` (skips without
`BRAVE_API_KEY`), `ddg`, `wikipedia`, `arxiv`, `pubmed`, `stackexchange`.
`--engine` selects a category or alias (compose with engine names; duplicates
removed):

| `--engine` | Engines |
|------------|---------|
| `general` | brave, ddg, mojeek, google, bing, tavily |
| `reference` | wikipedia, grokipedia |
| `academic` | arxiv, pubmed |
| `news` | hackernews |
| `'q&a'` | stackexchange *(quote it; `&` is a shell operator)* |
| `free` | all keyless engines |
| `all` | every registered engine |

Off by default (opt-in via config or `-e`): `mojeek`, `google`, `bing`,
`tavily`, `hackernews`, `grokipedia`. Keyed (auto-skip without their env var):
`brave`, `bing`, `tavily`. Inspect live state with `mst --list-engines`.

```bash
mst --engine academic "vision transformer 2025"
mst --engine academic,brave "transformer attention"   # compose with general web
mst --engine free "best espresso grinder"
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
