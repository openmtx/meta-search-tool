---
name: web-search
description: >-
  End-to-end web research combining `mst` (meta-search across 12 engines,
  RRF-ranked) with `webclaw` (local, single-binary page extraction). `mst`
  discovers and ranks URLs; optionally pipe interesting URLs through `webclaw`
  to read clean, LLM-ready page bodies. JS-rendered or bot-protected pages
  need `WEBCLAW_API_KEY` (cloud fallback); everything else runs locally with
  no key.
license: Apache-2.0
compatibility: Auto-installs pinned `mst` + `webclaw` to ~/.local/bin/ (on PATH); no key required by default.
metadata:
  version: "1.0.0"
  requires:
    mst: ">=0.3,<1.0"
    webclaw: ">=0.6,<1.0"
---

# web-search (mst → webclaw)

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

Installs to `~/.local/bin/` (ensure it is on your `PATH`). Downloads ~38 MB
total, SHA256-verified; each pinned to an exact tested version — mst v0.3.2,
webclaw v0.6.14:

```bash
curl -fsSL https://raw.githubusercontent.com/openmtx/meta-search-tool/master/skills/web-search/scripts/setup.sh | bash
```

It is a fast no-op if both pinned versions are already installed. Verify:

```bash
mst --version && webclaw --version
```

A keyed engine without its env var (e.g. `BRAVE_API_KEY`) is auto-skipped,
not an error — unless named in `--require`.

## Usage

### Just search — URLs/snippets only (cheap on context)

Use `mst` alone when you only need links, snippets, or quick answers:

```bash
# Cheapest: one result per line
mst --format jsonl --limit 10 "<query>"

# Full envelope with per-engine status + scores
mst --format json --limit 10 "<query>"

# Human-readable
mst --format text "<query>"
```

**Steer ranking and scope** with categories and weights:

```bash
mst --engine academic         "vision transformer 2025"
mst --engine academic,brave   "transformer attention"
mst --engine free             "best espresso grinder"
mst --weight brave=2.0        "tokio"
```

**Bare URLs** (project only the fields you need — no `jq` required):

```bash
mst --format text --fields url --limit 10 "<query>"
```

**Read query from stdin:**

```bash
printf '%s' "<long query>" | mst --stdin --format jsonl
```

Use `jsonl` when you just need hits; use `json` when you want `score`/`engines[]`
to reason about quality.

### Full research — search, then read page bodies

Combine `mst` (discover URLs) with `webclaw` (extract page content):

```bash
# 1. Meta-search to discover + rank URLs
mst --format text --fields url --limit 10 "<query>" > /tmp/urls.txt

# 2. Extract clean, LLM-ready content from those pages
webclaw --urls-file /tmp/urls.txt -f llm
```

One-liner (positional args; prefer `--urls-file` when the URL list is large):

```bash
mst --format text --fields url --limit 10 "<query>" | xargs webclaw -f llm
```

Want to review titles/snippets before fetching? Run
`mst --format text --limit 10 "<query>"` (readable) or `--format jsonl` (full
structured results), then extract only the URLs worth reading. Extraction is the
expensive step, so filter the URL list before feeding webclaw.

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
`brave`, `bing`, `tavily`. Inspect live state with `mst --list-engines`.

```bash
mst --engine academic "vision transformer 2025"
mst --engine academic,brave "transformer attention"   # compose with general web
mst --engine free "best espresso grinder"
```

## Choosing a webclaw output format

| Flag | Use when |
|------|----------|
| `-f llm` | **Default for research.** Token-optimized (~90% fewer tokens than HTML); best for feeding an LLM. |
| `-f markdown` | Clean, structure preserved. |
| `-f json` | Metadata (title, description, date, word_count) + content. **Note: not parsed tables.** |
| `-f text` | Plain text, minimal formatting. |
| `--metadata` | Append a metadata header to markdown/text. |

## Structured / typed JSON (instead of prose)

For known sites, webclaw's **vertical extractors** return deterministic typed
JSON (no LLM, no tokens):

```bash
webclaw extractors                                          # list all (~28)
webclaw vertical github_repo https://github.com/rust-lang/rust --raw | jq '{stars, forks}'
```

For arbitrary pages, LLM-based extraction (Ollama local/free first, else
`OPENAI_API_KEY` / `ANTHROPIC_API_KEY`):

```bash
webclaw <url> --extract-json '{"type":"object","properties":{"title":{"type":"string"},"price":{"type":"string"}}}'
```

## JavaScript-rendered / bot-protected pages

webclaw runs locally with **no API key** via HTTP + TLS-fingerprint
impersonation — fast, no browser. It **cannot render JavaScript**. For JS-heavy
or bot-protected pages:

- Set `WEBCLAW_API_KEY` → webclaw auto-falls-back to cloud (JS render + antibot).
  Force it with `--cloud` for sites that always need it.
- Without a key, such pages return an empty shell — detect near-empty output and
  skip the URL, or flag it for manual review.

## Content filtering

```bash
webclaw <url> --only-main-content                       # article/main only; drop nav/ads
webclaw <url> --include "article" --exclude "nav, footer, .sidebar"
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
- **Pagination** (best-effort, stateless): `mst --limit 10 --offset 10 "<query>"`

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

**`mst` (Tier 2 — tuning):** `--rrf-k` (ranking smoothing), `--require` (engine
must run or exit), `--strict` (exit 3 on any engine failure), `--timeout` (run
budget).

**`webclaw` (routine):** `[URLS]...`, `--urls-file`, `-f/--format`
(markdown\|llm\|json\|text), `--only-main-content`, `--include`, `--exclude`,
`--cloud`, `--metadata`, `vertical <name> <url>`, `extractors`, `--extract-json`,
`--version`.

See `mst --help` and `webclaw --help` for the full reference.
