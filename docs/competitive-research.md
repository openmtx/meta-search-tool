# Competitive Research — Meta-Search Landscape

Date: 2026-07-07 · Purpose: validate that `mst` has a real, unfilled niche before we build.

## TL;DR

There is **no widely-used, CLI-first, single-binary meta-searcher that gives the
caller transparent control over ranking quality.** The space splits into four
camps, all of which miss at least one of: scriptability, ranking control, or
zero-infra deployment. **`mst`'s wedge is exactly that combination.**

## Landscape Map

| Camp | Examples | Strength | Gap for an AI harness |
|------|----------|----------|------------------------|
| **Self-hosted web metasearch** | SearXNG (33.6k★), searx, Whoogle, metasearch2 (Rust) | Broad engines, privacy, free | Opaque merging; ranking is basic concatenation/sort, not tunable; web-app UX, not scriptable |
| **AI search APIs (paid, black-box)** | Tavily, Exa, SerpAPI, Kagi, You.com | High quality, LLM-friendly | Quality is *their* black box; cost; you can't reweight or mix providers; vendor lock-in |
| **Single-provider MCP servers** | tavily-mcp (2.2k★), exa-mcp (4.7k★), duckduckgo-mcp (1.3k★), kagimcp, serpapi-mcp | Drop-in for MCP-supporting harnesses | One engine each — no meta-merge, no cross-engine dedup/RRF |
| **Metasearch-as-a-service for agents** | MetaSearchMCP, agent-search | Agent JSON contract, dedup, broad sources | **Server-first** (run FastAPI/HTTP/MCP), no per-engine weighting, ranking quality explicitly *on their roadmap* |

## Closest Competitors (deep dive)

### 1. `gefsikatsinelou/MetaSearchMCP` — 51★, Python/FastAPI, active
The **single closest project**. Aggregates 50+ providers (web, academic, dev,
finance, knowledge), normalizes to a stable JSON schema, dedupes, isolates
per-provider timeouts, exposes HTTP API + MCP over stdio. Tag-based provider
selection (`web`, `academic`, `code`, …).

**What it lacks (your pain point, confirmed in their own README):**
- **No per-engine weights.** `MAX_RESULTS_PER_PROVIDER` + `max_total_results`
  are caps, not quality controls.
- **Ranking is not transparent** — results carry `rank` and `provider` but no
  inspectable score; their roadmap literally says: *"Better scoring and ranking
  signals across providers."*
- **Server-first**, not a one-shot CLI. You run a FastAPI/Uvicorn process.
- Python, not a single distributable binary.

### 2. `mat-1/metasearch2` — 167★, Rust, active
A "cute" self-hostable metasearch engine. Config-driven engine definitions,
decent breadth, fast. But it's a **web app/server**, ranking is shallow, and it
is not designed as a pipeable agent tool.

### 3. `karust/openserp` — 961★, Go
Self-hosted **SERP** API — scrapes Google/Bing single-engine per request. **Not
meta-merge** (no cross-engine fusion). Useful reference for Go scraping patterns.

### 4. SearXNG — 33.6k★, Python (your current tool)
The dominant player. Excellent breadth/privacy, but:
- Merging is opaque and shallow (engine results are largely concatenated with
  light scoring; not RRF, not weighted by the caller).
- No way for an agent to say "weight Brave higher this query" or to read a
  score it can reason about.
- Designed for browser UX, not for a harness driving quality.

## The Gap `mst` Fills

> An AI harness needs to **influence and inspect** search quality per query,
> from a tool that **starts in milliseconds with no server to run.**

| Requirement | SearXNG | MetaSearchMCP | Tavily/Exa | **`mst`** |
|---|:---:|:---:|:---:|:---:|
| Single static binary, no server | ✗ | ✗ | ✗ | ✅ |
| CLI + stdin/stdout pipeable | partial | ✗ | ✗ | ✅ |
| JSON-first, stable agent schema | partial | ✅ | ✅ | ✅ |
| **Per-engine weights, set per-call** | ✗ | ✗ | ✗ | ✅ |
| **Transparent score** in output (RRF) | ✗ | ✗ | ✗ | ✅ |
| Mix free + paid engines, dedup | ✅ | ✅ | ✗ | ✅ |
| Cross-engine dedup + re-rank | weak | ✅ | n/a | ✅ |

The **two bold rows are the real differentiators** and they map directly onto
your complaint: "the AI harness can't decide the quality of the search." With
`mst`, the harness passes `--weight brave=2.0 --exclude ddg --limit 20` and gets
back `score` it can reason about. Nobody
else offers that.

## Implications & Recommendations

1. **Keep the scope to web/general search.** Do not chase MetaSearchMCP's 50+
   providers (academic/finance/dev). `mst` wins on *quality control of web
   search*, not breadth. Ship `ddg`, `wikipedia`, `brave` first.
2. **Make ranking the hero feature.** Document the RRF + weights model loudly;
   expose `score` and contributing `engines[]` in every result. This is the one
   thing the whole category under-builds.
3. **Skip a bespoke MCP server; ship a skill instead.** The single-engine MCP
   servers (tavily-mcp, exa-mcp, …) exist because their products are services,
   not CLIs. `mst` is already a JSON CLI any harness can shell out to, and an
   Agent Skills skill wraps it for skill-aware harnesses — together covering
   every harness. A bespoke `mst mcp` would re-express the same flag/exit/output
   contract as a second, drift-prone surface for a narrow audience. Build one
   only if real demand from bash-gated or MCP-only harnesses appears, and then
   generate it from a `--help-json` schema rather than by hand.
4. **Do not compete on being a SearXNG replacement.** SearXNG is a UI/privacy
   product with 33k★ and a community. `mst` is a *tool* for agents and power
   users who want control — a different buyer.
5. **Differentiation phrasing for the README:** *"SearXNG-style aggregation,
   Tavily-style agent ergonomics, but the ranking is yours to control — from a
   single binary you pipe into anything."*

## Conclusion

**Proceed.** The niche (CLI-first, single-binary, transparent + controllable
meta-search ranking for agents) is real and unoccupied. The risk is not
competition; it's scope creep. Stay narrow on web search, make ranking
observable and tunable, and let a skill (not a bespoke server) carry the agent
surface.
