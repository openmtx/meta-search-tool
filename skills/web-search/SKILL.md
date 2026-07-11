---
name: web-search
description: >-
  Search the web with `mst`, a single-binary meta-search CLI. Fans out to
  multiple engines concurrently, merges results with tunable RRF ranking,
  emits JSON. Use for current web info, facts, docs, or URLs — not for
  reading page bodies (call a fetch tool on returned URLs).
license: Apache-2.0
compatibility: Requires `mst` on PATH. The skill auto-detects and installs if missing.
---

# web-search (mst)

## Setup

Before running any search, the skill follows this flow:

1. **Try running `mst`.** If it works, proceed.
2. **If not on PATH**, download and install a prebuilt binary from
   [GitHub Releases](https://github.com/openmtx/meta-search-tool/releases) using the one-shot script below (discovers the exact asset name via the GitHub API, so no guessing about version prefixes or extraction paths):

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

3. **Verify**:
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
