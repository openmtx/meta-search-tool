# mst — Meta Search Tool

> One prompt → concurrent search across many engines → one merged, re-ranked,
> JSON result you (or your AI agent) can actually control.

`mst` is a command-line meta-search tool. Given a single query, it fans out to
multiple search engines **concurrently**, merges and re-ranks the results with
**transparent scoring**, and emits **JSON by default** — ready to pipe into a
script, a downstream LLM, or an agent harness.

## The problem

Existing options all force a trade-off:

- **SearXNG** (33k★) and friends aggregate broadly, but merging is **opaque** —
  you can't see or influence how results are ranked, and there's no way for an
  agent to say "trust Brave more for *this* query."
- **Tavily / Exa / SerpAPI** give high-quality results, but the ranking is a
  paid **black box**; you can't reweight, mix providers, or inspect scores.
- **Single-engine MCP servers** only hit one source — no cross-engine
  deduplication or fusion at all.

In short: **no widely-used tool lets the caller *decide and inspect* search
quality per query, from something you just run.** An AI harness has no lever.

## What `mst` does

- **Concurrent fan-out** to all enabled engines with per-engine and global
  timeouts; a failing engine never aborts the run.
- **Transparent ranking** via Reciprocal Rank Fusion, with **per-engine
  weights** set in config or per call (`--weight brave=2.0`).
- **Every result carries an inspectable `score` and the list of engines that
  contributed**, so an agent can reason about (and tune) quality.
- **JSON-first** output, plus `jsonl`, `text`, and `table`.
- **Single static binary**, no server to run, no runtime dependencies.

```bash
mst "rust async runtime comparison"
mst --engine brave,ddg --weight brave=2.0 --limit 20 "best espresso grinder"
```

## Quick Start

> `mst` is pre-alpha (spec complete; implementation starting). Once published:

```bash
go install github.com/<owner>/mst@latest   # or build from source: go build ./cmd/mst
```

Search and get JSON by default:

```bash
mst "rust async runtime comparison"                  # JSON to stdout
mst --engine brave,ddg --weight brave=2.0 "tokio"    # steer ranking per call
mst -f text "how does riscv paging work"             # human-readable
```

Example output (truncated):

```json
{
  "query": "rust async runtime comparison",
  "took_ms": 6109,
  "results": [
    {
      "rank": 1,
      "title": "Tokio vs async-std ...",
      "url": "https://example.com/tokio-vs-async-std",
      "source": "example.com",
      "snippet": "...",
      "score": 0.0331,
      "engines": ["brave", "ddg"]
    }
  ]
}
```

Every result carries `score` and `engines[]` — the lever an agent gets that no
other tool offers.

## Using `mst` from an LLM harness

`mst` is a plain CLI that prints JSON, so **any** LLM harness that can shell
out and parse JSON can drive it directly — there is no harness lock-in. It is
consumed through whichever path a harness supports:

- **Direct shell** — run `mst …` and parse stdout. Works everywhere; this is
  the baseline.
- **[Agent Skills](https://agentskills.io/)** — a cross-harness skill standard.
  `mst` ships a ready-made skill wrapping its stable, agent-facing flag set:
  [`skills/web-search/SKILL.md`](./skills/web-search/SKILL.md). Install it by
  copying or symlinking the `skills/web-search` directory into your harness's
  skill location (e.g. `~/.agents/skills/` per the standard) — see your
  harness's docs for the exact path or settings key.

The canonical invocation a skill definition, tool schema, or system prompt
should reach for:

```bash
mst --format jsonl --limit 10 "rust async runtime comparison"
```

## Why it's worth trying

The niche is real and unoccupied: a **CLI-first, single-binary meta-searcher
with controllable, observable ranking**. The closest project
([MetaSearchMCP](https://github.com/gefsikatsinelou/MetaSearchMCP), 51★) is
server-first and lists *"better scoring and ranking signals"* on its own
roadmap — confirming the gap. `mst` bets that **ranking control is the feature**,
not breadth, and that agents (and power users) want a tool they can pipe, not a
service they must host.

## Status

Early / specification stage (spec complete; implementation starting). See:

- [`SPEC.md`](./docs/SPEC.md) — full design (CLI, engines, ranking, config).
- [`docs/competitive-research.md`](./docs/competitive-research.md) — landscape
  analysis and differentiation.

Written in **Go**.

## License

Apache-2.0. See [`LICENSE`](./LICENSE).

Contributions welcome — open an issue first to discuss scope. `mst` stays a
*tool* (CLI-first, narrow on web search), not a hosted service or a SearXNG
replacement.
