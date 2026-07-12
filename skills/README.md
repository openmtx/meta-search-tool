# Agent Skills

`mst` ships a ready-made [Agent Skill](https://agentskills.io/) that teaches an
AI agent how to search the web and read pages with the CLI. It is
self-contained, auto-installing, and needs **no API key** for the common path.

| Skill | Pipeline | Tools | Reach for it when… |
|-------|----------|-------|--------------------|
| **[web-search](./web-search/SKILL.md)** | meta-search → URLs / titles / snippets → **optionally extract page bodies** | `mst` + [`webclaw`](https://github.com/0xMassi/webclaw) | You need current web info, facts, docs, URLs — and optionally want to read the full page content. |

Just need links and snippets? Call `mst` alone (cheap on context). Need to read
the actual pages? Pipe through `webclaw` for clean, LLM-ready content.

## Prerequisites

- **Binaries auto-install** to `~/.local/bin/` (ensure it is on your `PATH`).
  `mst` from [openmtx/meta-search-tool](https://github.com/openmtx/meta-search-tool)
  and `webclaw` from [0xMassi/webclaw](https://github.com/0xMassi/webclaw). The
  skill resolves and SHA256-verifies the right release itself. See
  [DEPENDENCIES.md](./DEPENDENCIES.md) for the version pinning & verification
  approach.
- **No key required by default.** `mst`'s default engines (`ddg`, `google`) and
  `webclaw`'s local extraction are keyless.
- **Optional keys** (extend coverage, don't unlock the base path):
  - `BRAVE_API_KEY` — adds the `brave` general-web engine to `mst`.
  - `WEBCLAW_API_KEY` — cloud fallback for JavaScript-rendered or bot-protected
    pages in `webclaw` (force with `--cloud`).

---

## web-search — discovery & optional page extraction

Fan out to many engines concurrently, fuse results with tunable RRF ranking,
emit JSON. Returns URLs/titles/snippets — optionally pipe through `webclaw` for
full page content.

### Just search (URLs/snippets only, cheap on context)

```bash
# Cheapest: one result per line (jsonl)
mst --format jsonl --limit 10 "rust async runtime comparison"

# Full envelope with per-engine status + scores (reason about quality)
mst --format json  --limit 10 "rust async runtime comparison"

# Human-readable
mst --format text "how does riscv paging work"
```

**Steer ranking and scope** with categories and weights:

```bash
mst --engine academic          "vision transformer 2025"   # arxiv + pubmed
mst --engine academic,brave    "transformer attention"     # + general web
mst --engine free              "best espresso grinder"     # all keyless engines
mst --weight brave=2.0         "tokio"                     # boost an engine
```

**Bare URLs** (project only the fields you need — no `jq` required):

```bash
mst --format text --fields url --limit 10 "<query>"
```

### Full research — search, then read page bodies

```bash
# 1. Discover + rank URLs (bare URLs via field projection)
mst --format text --fields url --limit 10 "<query>" > /tmp/urls.txt

# 2. Extract LLM-optimized content from those pages
webclaw --urls-file /tmp/urls.txt -f llm
```

One-liner (positional args; prefer `--urls-file` for large URL lists):

```bash
mst --format text --fields url --limit 10 "<query>" | xargs webclaw -f llm
```

**Pick a webclaw format** for the job:

| Flag | Best for |
|------|----------|
| `-f llm` | **Default for research.** Token-optimized; best for feeding an LLM. |
| `-f markdown` | Clean structure preserved. |
| `-f json` | Metadata (title, date, word_count) + content. |
| `--only-main-content` | Article/main only — drop nav, ads, sidebars. |

**Structured data from known sites** via webclaw's vertical extractors
(deterministic typed JSON, no LLM, no tokens):

```bash
webclaw extractors                                          # list all (~28)
webclaw vertical github_repo https://github.com/rust-lang/rust --raw | jq '{stars, forks}'
```

---

## Using this skill in an agent (pi)

This follows the [Agent Skills](https://agentskills.io/) standard and works in
[pi](https://github.com/earendil-works/pi-coding-agent) and other compatible
harnesses.

**Progressive disclosure — low system-prompt cost.** The skill's
`description` (~275 chars) sits in the system prompt; the full `SKILL.md` body
is loaded on-demand only when a task matches.

**Install** (pi discovers any directory containing a `SKILL.md`):

```bash
# Global (available everywhere)
cp -r web-search ~/.pi/agent/skills/

# …or per-project
mkdir -p .pi/skills && cp -r web-search .pi/skills/

# …or one-off
pi --skill ./web-search
```

**Invoke** once installed:

```text
/skill:web-search
```

…or simply describe the task — the agent loads the matching skill automatically.

---

## License

These skills are Apache-2.0 (same as `mst`). `webclaw` is a separate upstream
project; see [0xMassi/webclaw](https://github.com/0xMassi/webclaw) for its license.
