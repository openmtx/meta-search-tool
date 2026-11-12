# mst — Meta Search Tool

[![Release](https://github.com/openmtx/meta-search-tool/actions/workflows/release.yml/badge.svg)](https://github.com/openmtx/meta-search-tool/actions/workflows/release.yml)

A CLI meta-search tool designed for LLM harnesses. Fan out to many engines
concurrently, get one merged JSON result with transparent scoring. No server,
no setup.

## The problem

Your agent needs web search. [SearXNG](https://github.com/searxng/searxng) is
great, but self-hosting is a project and public instances are overloaded and
unreliable. Commercial search APIs work but lock you into one source with
opaque ranking.

`mst` gives you a single static binary that fans out to many engines
concurrently, fuses results with tunable RRF ranking, and prints JSON to
stdout. No daemon, no runtime dependencies, no setup.

## Quick Start

```bash
# Install once, run anywhere
go install github.com/openmtx/meta-search-tool/cmd/mst@latest

mst "rust async runtime comparison"               # JSON → stdout
mst -f text "how does riscv paging work"          # human-readable
mst --limit 20 --weight brave=2.0 "tokio"          # steer ranking
```

Example output:

```json
{
  "query": "rust async runtime comparison",
  "took_ms": 6109,
  "results": [
    {
      "rank": 1,
      "title": "Tokio vs async-std ...",
      "url": "https://example.com/tokio-vs-async-std",
      "score": 0.0331,
      "engines": ["brave", "ddg"]
    }
  ]
}
```

## Installation

`mst` is a single static binary with no runtime dependencies. Pick either path.

### Prebuilt binary (recommended — no Go needed)

1. Download the archive for your platform from
   [GitHub Releases](https://github.com/openmtx/meta-search-tool/releases).
   Archives are named `mst-<tag>-<os>-<arch>.tar.gz` (Linux/macOS) or `.zip`
   (Windows), where `os` is `linux`, `darwin`, or `windows` and `arch` is
   `amd64` or `arm64` — e.g. `mst-v0.5.0-linux-amd64.tar.gz`.
2. Extract and put `mst` on your PATH:

   ```bash
   tar xzf mst-v0.5.0-linux-amd64.tar.gz
   mkdir -p ~/.local/bin
   install -m 0755 mst-v0.5.0-linux-amd64/mst ~/.local/bin/mst   # any dir on $PATH
   ```

   On Windows, unzip the archive and move `mst.exe` to a directory on `%PATH%`.

3. *(Optional)* Verify against the `SHA256SUMS.txt` shipped with the release:

   ```bash
   sha256sum -c --ignore-missing SHA256SUMS.txt   # macOS: shasum -a 256 -c
   ```

### From source (needs Go 1.26+)

```bash
go install github.com/openmtx/meta-search-tool/cmd/mst@latest
```

### Verify

```bash
mst --version        # e.g. mst v0.5.0
mst --list-engines   # engines, status, effective weights
```

## What `mst` gives you

- **Concurrent fan-out** to all enabled engines with per-engine timeouts; a
  failing engine never aborts the run.
- **Transparent ranking** via Reciprocal Rank Fusion with per-engine weights
  (`--weight brave=2.0`) and tunable smoothing (`--rrf-k`).
- **JSON by default** — every result carries `score` and the list of engines
  that contributed. Also supports JSONL, Text, and Table output.
- **Agent Skills ready** — ships a ready-made
  [Agent Skill](https://agentskills.io/) wrapping the stable flag set:
  [`skills/web-search/SKILL.md`](./skills/web-search/SKILL.md)
  (search → URLs, optionally extract page bodies via
  [`webclaw`](https://github.com/0xMassi/webclaw)).
  Install in one command, no prompt engineering.
- **Single binary** — download the right platform build from GitHub Releases
  and run. No Go toolchain, no runtime deps, no server.
- **Engine categories** — `--engine academic`, `--engine free`, `--engine all`
  select groups of engines at once.
- **Raw & unmerged modes** — `--raw` and `--no-merge` for full control.

## Status

**v0.5** — 14 engines across 4 reliability tiers, 500+ tests, all spec flags
implemented, `-race` clean.

| Tier | Engines |
|------|---------|
| **A** — reliable general web | `brave` (keyed), `mojeek` (keyless; Altcha CAPTCHA detected, disabled), `ddg` (keyless, on by default) |
| **B** — knowledge & reference | `wikipedia`, `arxiv`, `pubmed`, `europepmc`, `stackexchange`, `hackernews`, `grokipedia` |
| **C** — general web | `google` (official API + public CSE), `bing` (keyed) |
| **D** — AI-oriented | `tavily` (keyed), `jina` (keyed) |

See [`docs/SPEC.md`](./docs/SPEC.md) for full design details.

## License

Apache-2.0. See [`LICENSE`](./LICENSE).

## Acknowledgments

Many of the ideas in `mst` come from **[SearXNG](https://github.com/searxng/searxng)**,
the leading self-hosted metasearch engine. Its engine implementations — the
DuckDuckGo no-JS form POST, the Google CSE public token path, the Mojeek
scraping selectors, and many more — served as the reference design for `mst`'s
keyless engine integrations, and the default engine set follows SearXNG's
conventions to reduce surprise for users familiar with it.

`mst` is not a SearXNG replacement or fork. We made different trade-offs:

- **One Google, not several.** SearXNG ships separate Google "engines"
  (`google`, `google_cse`, API variants). `mst` merges the usable paths into a
  single `google` engine with automatic mode selection (official API → public
  CSE), so a Google search is always a Google search. The HTML scrape fallback
  was retired in v0.5.
- **AI-oriented providers.** `mst` adds `tavily` (answer mode) and `jina`
  (AI search) alongside the keyless knowledge engines — options SearXNG does
  not provide.

`mst` is a complementary CLI-first tool inspired by the same problems SearXNG
solves, optimized for scriptable use and agent-driven workflows.
