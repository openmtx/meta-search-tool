---
name: web-search
description: >-
  Search the web with `mst`, a single-binary meta-search CLI. Fans out to
  multiple engines concurrently, merges results with tunable RRF ranking,
  emits JSON. Use for current web info, facts, docs, or URLs — not for
  reading page bodies (call a fetch tool on returned URLs).
license: Apache-2.0
compatibility: Requires `mst` on PATH. Get it via `source <(curl -sL https://raw.githubusercontent.com/openmtx/meta-search-tool/master/skills/web-search/setup.sh)` or `go install github.com/openmtx/meta-search-tool/cmd/mst@latest`.
---

# web-search (mst)

## Setup

```bash
# Check if already on PATH:
mst --list-engines   # shows available engines and their status

# Or download the binary for this platform (cached, re-run to update):
source <(curl -sL https://raw.githubusercontent.com/openmtx/meta-search-tool/master/skills/web-search/setup.sh)

# Install from source (needs Go):
go install github.com/openmtx/meta-search-tool/cmd/mst@latest
```

If download/install fails, no fallback — mst is required. Get the binary from
[GitHub Releases](https://github.com/openmtx/meta-search-tool/releases).

## Usage

**Preferred (results only):**
```bash
mst --format jsonl --limit 10 "<query>"
```

**Full envelope (with per-engine status):**
```bash
mst --format json --limit 10 "<query>"
```

**Steer ranking per query:**
```bash
mst --engine brave,wikipedia --weight brave=2.0 "<query>"
```

**Pagination (best-effort):**
```bash
mst --limit 10 --offset 10 "<query>"
```

**Read query from stdin:**
```bash
printf '%s' "<long query>" | mst --stdin --format jsonl
```

## Profiles — pick the right engines

| Task | `--engine` | Targets |
|------|------------|---------|
| Broad web search | *(default)* | brave, bing, google, ddg, mojeek, tavily |
| Facts / reference | `reference` | wikipedia, grokipedia |
| Research papers | `academic` | arxiv, pubmed |
| Tech news | `news` | hackernews |
| Programming Q&A | `'q&a'` | stackexchange |
| Zero-setup (keyless) | `free` | all keyless engines |
| Maximum coverage | `all` | everything |

```bash
mst --engine academic "vision transformer 2025"
mst --engine academic,brave "transformer attention"       # compose with general web
mst --engine free "best espresso grinder"
```

## Errors

| Exit | Meaning | Action |
|------|---------|--------|
| 0 | success | — |
| 1 | zero results | broaden query, add engines, raise `--limit` |
| 2 | bad flags / missing key | fix command |
| 3 | some engines failed | stdout still has results; `--exclude` the flaky one |

Error details are JSON on stderr. Exit 1/3: stdout is still valid JSON.

## Flags

`<query>` (positional), `--stdin`, `-e/--engine`, `--exclude`, `-n/--limit`,
`--offset`, `-f/--format`, `--weight`, `--list-engines`, `--version`.
Avoid debug flags (`--raw`, `--no-merge`, `--rrf-k`, `--require`, `--strict`,
`--timeout`, `--per-engine-timeout`, `--config`, `--verbose`) unless diagnosing
mst itself.
