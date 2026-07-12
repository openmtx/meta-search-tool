---
name: web-search
description: >-
  End-to-end web research combining `mst` (meta-search across 12 engines, RRF-ranked)
  with `webclaw` (local page extraction). `mst` discovers URLs; `webclaw` reads them.
  Auto-installs pinned versions to ~/.local/bin/ on first use. No key required by default;
  set WEBCLAW_API_KEY for JS-rendered / bot-protected pages.
license: Apache-2.0
compatibility: Auto-installs pinned `mst` + `webclaw` to ~/.local/bin/ (on PATH); no key required by default.
metadata:
  version: "1.0.0"
  requires:
    mst: ">=0.3,<1.0"
    webclaw: ">=0.6,<1.0"
---

# web-search (mst → webclaw)

**Rule: always chain `mst` → `webclaw`.** Never pass a bare URL to `webclaw` unless discovered outside `mst`.

## Setup

```bash
mst --version && webclaw --version || \
  curl -fsSL https://raw.githubusercontent.com/openmtx/meta-search-tool/master/skills/web-search/scripts/setup.sh | bash
```

## Core Pipeline

```bash
# Preferred: one-liner
mst --format text --fields url --limit 10 "<query>" | xargs webclaw -f llm

# Two-step (review before fetching)
mst --format text --fields url --limit 10 "<query>" | head -5 | xargs webclaw -f llm
```

- **`mst`** — discovery + ranking. Returns URLs/titles/snippets from multiple engines.
- **`webclaw`** — extraction. Fetches URLs, returns clean markdown/LLM-ready content.
- `mst` alone = cheap (URLs/snippets). `mst → webclaw` = full research.

## Quick Usage

```bash
# Just search (no webclaw)
mst --format jsonl --limit 10 "<query>"        # one hit per line
mst --format json --limit 10 "<query>"         # full envelope with scores
mst --format text --fields url --limit 10 "<query>"   # bare URLs only
mst --stdin --format jsonl                      # read query from stdin

# Steer engines: academic, reference, news, 'q&a', free (all keyless), all
mst --engine academic "vision transformer 2025"
mst --engine academic,brave "transformer attention"
```

**Default engines:** `ddg`, `google`, `brave` (skipped without `BRAVE_API_KEY`). Others are opt-in.
**Discover engines:** `mst --list-engines` shows every registered engine, its keyless/keyed status, and whether it's enabled for the next run.

## webclaw Formats

| Flag | Use when |
|------|----------|
| `-f llm` | **Default.** Token-optimized (~90% fewer tokens than HTML); best for feeding an LLM. |
| `-f markdown` | Clean, structure preserved. |
| `-f text` | Plain text, minimal formatting. |
| `-f json` | Metadata (title, date, word_count) + markdown content (not parsed tables). |
| `--metadata` | Append metadata header to markdown/text output. |
| `--only-main-content` | Drop nav, ads, sidebars; article/main content only. |

**Structured data** from known sites via webclaw's built-in extractors (deterministic typed JSON, no LLM, no tokens):

```bash
webclaw extractors                    # list all (~28 available)
webclaw vertical github_repo <url>    # e.g. stars, forks as typed JSON
```

## Gotchas

- `webclaw --stdin` reads HTML, not URLs. Use positional args or `--urls-file`.
- Local webclaw has no JS rendering. Near-empty output = try with `WEBCLAW_API_KEY` + `--cloud`.
- `-f json` is metadata + markdown, not parsed tables.
- **Pagination:** `mst --limit 10 --offset 10 "<query>"`

## Errors

`mst` exit codes: 0=success, 1=zero results (stdout still valid — still parse it), 2=bad flags/config (no output), 3=`--strict`/`--require` failure (stdout still valid).
webclaw: non-zero = fetch failure for that URL.

**Rule:** parse `mst` stdout unconditionally on any exit code other than 2.
