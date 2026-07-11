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
compatibility: Requires `mst` and `webclaw` on PATH. The skill auto-detects and installs both if missing (prebuilt binaries from GitHub Releases).
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

Before running research, ensure both are on PATH. For each: try running it; if
missing, install the prebuilt binary from GitHub Releases. Both install to
`~/.local/bin`.

### 1. `mst` (meta-search) — [openmtx/meta-search-tool](https://github.com/openmtx/meta-search-tool)

Verify: `mst --version`. If not on PATH, install:

```bash
# Detect architecture and OS
ARCH=$(uname -m)
OS=$(uname -s | tr '[:upper:]' '[:lower:]')

# Map uname outputs to GitHub release names
case "$ARCH" in
  x86_64)  ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
esac

# Find the correct asset name from the latest release via the GitHub API
ASSET=$(curl -fsSL "https://api.github.com/repos/openmtx/meta-search-tool/releases/latest" \
  | grep '"name":' | grep "${OS}-${ARCH}" | head -1 \
  | sed 's/.*"\(mst-[^\"]*\)".*/\1/')

if [ -z "$ASSET" ]; then
  echo "ERROR: No release asset found for ${OS}-${ARCH}" >&2
  exit 1
fi

# Download and install in one shot to ~/.local/bin (already on PATH)
INSTALL_DIR="$HOME/.local/bin"
mkdir -p "$INSTALL_DIR"
TMPDIR=$(mktemp -d)
curl -fsSL "https://github.com/openmtx/meta-search-tool/releases/latest/download/${ASSET}" -o "${TMPDIR}/release.tar.gz"
tar xzf "${TMPDIR}/release.tar.gz" -C "${TMPDIR}"
# The archive contains a subdirectory (mst-vX.Y.Z-<os>-<arch>/mst) — find the binary
BINARY=$(find "${TMPDIR}" -maxdepth 2 -type f -name 'mst' | head -1)
mv "$BINARY" "${INSTALL_DIR}/mst"
chmod +x "${INSTALL_DIR}/mst"
rm -rf "${TMPDIR}"
```

### 2. `webclaw` (extractor) — [0xMassi/webclaw](https://github.com/0xMassi/webclaw)

Verify: `webclaw --version`. If not on PATH, install:

```bash
# Detect architecture and OS
ARCH=$(uname -m)
OS=$(uname -s | tr '[:upper:]' '[:lower:]')

# Map to webclaw's Rust release target triple
TARGET=""
case "$OS" in
  darwin)
    case "$ARCH" in
      x86_64)           TARGET="x86_64-apple-darwin" ;;
      aarch64|arm64)    TARGET="aarch64-apple-darwin" ;;
    esac ;;
  linux)
    case "$ARCH" in
      x86_64)           TARGET="x86_64-unknown-linux-gnu" ;;
      aarch64|arm64)    TARGET="aarch64-unknown-linux-gnu" ;;
    esac ;;
esac

if [ -z "$TARGET" ]; then
  echo "ERROR: Unsupported OS/arch for webclaw: ${OS}/${ARCH}" >&2
  exit 1
fi

# Find the exact asset for this target via the GitHub API
ASSET=$(curl -fsSL "https://api.github.com/repos/0xMassi/webclaw/releases/latest" \
  | grep '"name":' | grep "${TARGET}" | head -1 \
  | sed 's/.*"\(webclaw-[^\"]*\)".*/\1/')

if [ -z "$ASSET" ]; then
  echo "ERROR: No webclaw release asset for ${TARGET}" >&2
  exit 1
fi

# Download and install the CLI binary (archive also holds webclaw-mcp / webclaw-server, not needed here)
INSTALL_DIR="$HOME/.local/bin"
mkdir -p "$INSTALL_DIR"
TMPDIR=$(mktemp -d)
curl -fsSL "https://github.com/0xMassi/webclaw/releases/latest/download/${ASSET}" -o "${TMPDIR}/release.tar.gz"
tar xzf "${TMPDIR}/release.tar.gz" -C "${TMPDIR}"
BINARY=$(find "${TMPDIR}" -maxdepth 2 -type f -name 'webclaw' | head -1)
mv "$BINARY" "${INSTALL_DIR}/webclaw"
chmod +x "${INSTALL_DIR}/webclaw"
rm -rf "${TMPDIR}"
```

### 3. Verify both

```bash
mst --version        # installed and on PATH
mst --list-engines   # engines, status, effective weights

webclaw --version    # installed and on PATH
webclaw --help       # full CLI reference
```

## Usage

### The pipeline: search → extract

```bash
# 1. Meta-search to discover + rank URLs (bare URLs via field projection — no jq needed)
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
webclaw extractors                                          # list all (~28)
webclaw vertical github_repo https://github.com/rust-lang/rust --raw | jq '{stars, forks}'
```

For arbitrary pages, LLM-based extraction (Ollama local/free first, else
`OPENAI_API_KEY` / `ANTHROPIC_API_KEY`):

```bash
webclaw <url> --extract-json '{"type":"object","properties":{"title":{"type":"string"},"price":{"type":"string"}}}'
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

See `mst --help` and `webclaw --help` for the full reference.
