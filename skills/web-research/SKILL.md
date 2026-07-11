---
name: web-research
description: >-
  End-to-end web research combining `mst` (meta-search across multiple engines,
  RRF-ranked) with `webclaw` (local, single-binary page extraction). `mst`
  discovers and ranks URLs; `webclaw` fetches those pages and returns clean,
  LLM-ready markdown/text. Use for researching a topic — search, then read the
  page bodies. JS-rendered or bot-protected pages need `WEBCLAW_API_KEY` (cloud
  fallback); everything else runs locally with no key.
license: Apache-2.0
compatibility: Auto-installs pinned `mst` + `webclaw` to ~/.cache/agent-skills/web-research/bin/ (not PATH); no key required by default.
metadata:
  version: "1.0.0"
  requires:
    mst: ">=0.3,<1.0"
    webclaw: ">=0.6,<1.0"
---

# web-research (mst → webclaw)

Two tools, one pipeline:

- **`mst`** — meta-search. Fans out to multiple engines concurrently, merges
  results with tunable RRF ranking, emits JSON. **Discovery + ranking only** —
  it returns URLs/titles/snippets, not page bodies.
- **`webclaw`** — local page extraction. Fetches URLs and returns clean,
  LLM-ready content (markdown / token-optimized text / metadata). Single binary,
  no API key for the common path. **Extraction only** — it has no meta-search.

They are complementary: `mst` finds URLs no single engine surfaces; `webclaw`
reads the pages fast and locally. Wire them as `mst → webclaw`.

## Setup

`mst` and `webclaw` are **not on PATH** — this skill keeps its own isolated
copies at `~/.cache/agent-skills/web-research/bin/`, so they never clash with
another skill's tools. Install or verify both (downloads ~38 MB total,
SHA256-verified; each pinned to an exact tested version — mst v0.3.2,
webclaw v0.6.14):

```bash
curl -fsSL https://raw.githubusercontent.com/openmtx/meta-search-tool/master/skills/web-research/scripts/setup.sh | bash
```

It prints the cache bin dir and is a fast no-op if both pinned versions are
already present. Then set variables for the examples below:

```bash
MST=~/.cache/agent-skills/web-research/bin/mst
WC=~/.cache/agent-skills/web-research/bin/webclaw
"$MST" --version && "$WC" --version
```

## Usage

Examples assume `MST` and `WC` are set as in Setup.

### The pipeline: search → extract

```bash
# 1. Meta-search to discover + rank URLs (bare URLs via field projection — no jq needed)
"$MST" --format text --fields url --limit 10 "<query>" > /tmp/urls.txt

# 2. Extract clean, LLM-ready content from those pages
"$WC" --urls-file /tmp/urls.txt -f llm
```

One-liner (positional args; prefer `--urls-file` when the URL list is large):

```bash
"$MST" --format text --fields url --limit 10 "<query>" | xargs "$WC" -f llm
```

Want to review titles/snippets before fetching? Run
`"$MST" --format text --limit 10 "<query>"` (readable) or `--format jsonl` (full
structured results), then extract only the URLs worth reading. Extraction is the
expensive step, so filter the URL list before feeding webclaw.

### Choosing a webclaw output format

| Flag | Use when |
|------|----------|
| `-f llm` | **Default for research.** Token-optimized (~90% fewer tokens than HTML); best for feeding an LLM. |
| `-f markdown` | Clean, structure preserved. |
| `-f json` | Metadata (title, description, date, word_count) + content. **Note: not parsed tables.** |
| `-f text` | Plain text, minimal formatting. |
| `--metadata` | Append a metadata header to markdown/text. |

### Structured / typed JSON (instead of prose)

For known sites, webclaw's **vertical extractors** return deterministic typed
JSON (no LLM, no tokens):

```bash
"$WC" extractors                                          # list all (~28)
"$WC" vertical github_repo https://github.com/rust-lang/rust --raw | jq '{stars, forks}'
```

For arbitrary pages, LLM-based extraction (Ollama local/free first, else
`OPENAI_API_KEY` / `ANTHROPIC_API_KEY`):

```bash
"$WC" <url> --extract-json '{"type":"object","properties":{"title":{"type":"string"},"price":{"type":"string"}}}'
```

### JavaScript-rendered / bot-protected pages

webclaw runs locally with **no API key** via HTTP + TLS-fingerprint
impersonation — fast, no browser. It **cannot render JavaScript**. For JS-heavy
or bot-protected pages:

- Set `WEBCLAW_API_KEY` → webclaw auto-falls-back to cloud (JS render + antibot).
  Force it with `--cloud` for sites that always need it.
- Without a key, such pages return an empty shell — detect near-empty output and
  skip the URL, or flag it for manual review.

### Content filtering

```bash
"$WC" <url> --only-main-content                       # article/main only; drop nav/ads
"$WC" <url> --include "article" --exclude "nav, footer, .sidebar"
```

## Notes / gotchas

- **`webclaw --stdin` reads HTML, not URLs.** Feed URLs via `--urls-file` or
  positional args — never pipe a URL list into `--stdin`.
- **`-f json` is metadata + markdown, not parsed tables.** For tabular data, use
  a vertical extractor or `--extract-json`.
- **Local extraction needs no API key**; only JS/antibot pages cost cloud credits.
- **No meta-search in webclaw.** Discovery is `mst`'s job — don't try to search
  with webclaw (its `search` subcommand is single-engine Serper, hosted/paid, and
  not a meta-search).
- **No page-body reading in `mst`.** `mst` returns URLs/snippets only — call
  webclaw on the URLs to read content.

## Errors

`mst` exit codes (JSON error object on stderr):

| Exit | Meaning | Action |
|------|---------|--------|
| 0 | success | — |
| 1 | zero results | broaden query, add engines, raise `--limit` |
| 2 | bad flags / config / missing key for a `--require`'d engine | fix command |
| 3 | a `--require`'d engine ran but failed, or `--strict` and an engine failed | stdout still valid; `--exclude` the flaky one |

Parse `mst` stdout unconditionally on any non-2 exit (exits 1 and 3 still emit a
complete JSON payload). webclaw: non-zero exit = fetch/extract failure for that
URL; check stderr and fall back to `--cloud` if a key is set.

## Flags

**`mst` (Tier 1 — routine):** `<query>` (positional), `--stdin`, `-e/--engine`,
`--exclude`, `-n/--limit`, `--offset`, `-f/--format`, `--fields` (result
projection, e.g. `--fields url`), `--weight`, `--list-engines`, `--version`.

**`webclaw` (routine):** `[URLS]...`, `--urls-file`, `-f/--format`
(markdown\|llm\|json\|text), `--only-main-content`, `--include`, `--exclude`,
`--cloud`, `--metadata`, `vertical <name> <url>`, `extractors`, `--extract-json`,
`--version`.

See `"$MST" --help` and `"$WC" --help` for the full reference.
