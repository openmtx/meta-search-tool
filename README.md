# mst — Meta Search Tool

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

## What `mst` gives you

- **Concurrent fan-out** to all enabled engines with per-engine timeouts; a
  failing engine never aborts the run.
- **Transparent ranking** via Reciprocal Rank Fusion with per-engine weights
  (`--weight brave=2.0`) and tunable smoothing (`--rrf-k`).
- **JSON by default** — every result carries `score` and the list of engines
  that contributed. Also supports JSONL, Text, and Table output.
- **Agent Skill ready** — ships a ready-made
  [Agent Skill](https://agentskills.io/) wrapping the stable flag set;
  [`skills/web-search/SKILL.md`](./skills/web-search/SKILL.md). Install in
  one command, no prompt engineering.
- **Single binary** — download the right platform build from GitHub Releases
  and run. No Go toolchain, no runtime deps, no server.
- **Engine categories** — `--engine academic`, `--engine free`, `--engine all`
  select groups of engines at once.
- **Raw & unmerged modes** — `--raw` and `--no-merge` for full control.

## Status

**v0.3** — 12 engines across 4 reliability tiers, 500+ tests, all spec flags
implemented, `-race` clean.

| Tier | Engines |
|------|---------|
| **A** — reliable general web | `brave` (keyed), `mojeek` (keyless), `ddg` (keyless, on by default) |
| **B** — knowledge & reference | `wikipedia`, `arxiv`, `pubmed`, `stackexchange`, `hackernews`, `grokipedia` |
| **C** — scraped general web | `google` (hybrid API + public CSE + scrape), `bing` (keyed) |
| **D** — AI-oriented | `tavily` (keyed, with `include_answer`/`search_depth`) |

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

- **One Google, not three.** SearXNG ships three separate Google "engines"
  (`google` — HTML scrape, `google_cse` — public CSE token, `google_api` —
  official API). `mst` merges all three into a single `google` engine with
  automatic mode selection (official API → public CSE → HTML scrape), so a
  Google search is always a Google search.
- **Tavily.** `mst` adds an AI-oriented search engine (`tavily`) that returns
  a synthesized answer alongside URL results — something SearXNG doesn't have.

`mst` is a complementary CLI-first tool inspired by the same problems SearXNG
solves, optimized for scriptable use and agent-driven workflows.
