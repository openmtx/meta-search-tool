# Skill Dependencies & Versioning

How `mst`'s Agent Skills declare and manage the CLI tools they drive (`mst`,
`webclaw`). This is the spec for the **skill-local dependency model** (Sprint
D.3.1). It is a *skill-level* concern — `mst` itself gains nothing but a clean
`--version` (see "mst's only role" below).

## Why skill-local (not PATH, not self-update)

End users (and agents) interact with the **skill**, not `mst` directly. The skill
encodes a specific CLI contract — which flags (`--fields`), formats, exit codes.
Two failure modes follow:

- A newer `mst` the skill wasn't authored against can change that contract.
- A newer skill against an old `mst` fails on missing flags/features.

So **the skill owns a versioned, isolated copy of each tool it needs.** There is
no shared PATH binary (eliminates the diamond-dependency conflict between
`web-search` and `web-research`) and **no `mst` self-updater** — "upgrade" means
re-running a skill's installer, which refreshes *that skill's* tool(s)
independently.

## The cache convention

Each tool a skill bundles lives under a single generic, harness-agnostic root:

```
${XDG_CACHE_HOME:-$HOME/.cache}/agent-skills/<skill>/bin/<tool>
```

- **`agent-skills/`** — generic root (named for the [Agent Skills](https://agentskills.io/)
  standard); any skill, any harness, ours or third-party, may use it.
- **`<skill>/`** — per-skill namespace → isolation, no cross-skill conflict.
- **`bin/<tool>`** — the binaries.

Concrete:

```
~/.cache/agent-skills/web-search/bin/mst
~/.cache/agent-skills/web-research/bin/mst
~/.cache/agent-skills/web-research/bin/webclaw
```

These are **cache** (re-fetchable; `setup.sh` recreates them). macOS note:
`${XDG_CACHE_HOME:-$HOME/.cache}` resolves to `~/.cache`, which works; the
pedantically-correct path is `~/Library/Caches/agent-skills/…`.

> **Why an absolute path, not a file inside the skill directory?** Portability.
> Verified: **opencode** does **not** expose the skill directory to the model
> (its `<available_skills>` lists only `name` + `description`, no `location`),
> and its skill system loads `SKILL.md` *text* with no skill-dir-relative command
> execution. **pi** *does** expose `<location>` and resolves relative paths, but
> relying on that would break opencode. A self-documented absolute cache path
> works identically in pi, opencode, Claude Code, and any future harness.

## Frontmatter — declaring the dependency

The Agent Skills standard frontmatter has no native version/dependency field, and
both pi and opencode **ignore unknown fields**. So declarations go in the
standard **`metadata:`** map (string-to-string), which both harnesses preserve:

```yaml
---
name: web-search
description: ...
metadata:
  version: "1.0.0"                 # this skill's own version
  requires:                        # nested: tool → semver range
    mst: ">=0.4,<1.0"
---
```

`web-research` declares both tools under the same `requires` map:

```yaml
metadata:
  version: "1.1.0"
  requires:
    mst: ">=0.4,<1.0"
    webclaw: ">=0.6,<1.0"
```

A nested `requires` map scales as a skill gains dependencies and reads cleanly.
`metadata` is for documentation/tooling/humans (and future harness features); the
actual enforcement is in `scripts/setup.sh` (below), which hardcodes the same
range it was authored against — no runtime YAML parsing needed.

## `scripts/setup.sh` — find-or-install (canonical)

Each skill ships a `scripts/setup.sh` — the **single source of truth** for
installing its tool(s). It pins an **exact tested version** (a `VERSION` literal
in the script, bumped only after re-testing) rather than resolving "highest in
range": a new upstream release must never auto-replace a skill's known-good
binary. `metadata.requires.<tool>` is the *compatibility range* (documentation +
runtime drift-check), **not** the release selector.

Because opencode does not expose the skill directory, SKILL.md does **not** call
`scripts/setup.sh` by relative path. It runs it via a stable raw URL — single
source of truth, cross-harness, no skill-dir needed:

```bash
curl -fsSL https://raw.githubusercontent.com/openmtx/meta-search-tool/master/skills/<skill>/scripts/setup.sh | bash
```

Contract:

1. **Find:** is `<cache>/bin/<tool>` present, executable, and the pinned version
   (`<tool> --version`)? If yes → fast no-op; print the path.
2. **Drift-check:** if present but a different version, verify it still satisfies
   `requires.<tool>`; otherwise reinstall the pinned version.
3. **Install:** download the **pinned** release over HTTPS, **SHA256-verify**
   against the release's `SHA256SUMS.txt`, and atomically place it (download to a
   temp file → `mv` onto the final path; mode `0755`). Atomic `mv` means a
   concurrent run never sees a half-written binary.
4. **Report:** print the resolved path + version to stderr; on any failure, one
   clear line + non-zero exit (never a partial install).

Idempotent: re-running with the pinned binary already present is a fast no-op.

## Runtime range check

When the skill runs, its setup (or a cheap pre-check) confirms the cached tool
still satisfies the declared range — catching drift if the user manually swapped
the binary. On mismatch: **one clear stderr line** and a remediation hint
(*"re-run the skill's setup"*), never a silent mid-search break.

## Cross-harness portability (verified)

| Harness | Exposes skill dir / `<location>`? | Resolves skill-relative paths? | Works with absolute cache path? |
|---------|-----------------------------------|--------------------------------|---------------------------------|
| **pi** | ✅ yes | ✅ yes | ✅ yes |
| **opencode** | ❌ no (name+description only) | ❌ not documented | ✅ yes |
| **Claude Code** | (`.claude/skills`) | partial | ✅ yes |

Because the SKILL.md points the model at an **absolute** cache path, the model
needs nothing from the harness — it works everywhere.

## mst's only role

`mst --version` already prints `mst <version>` (parseable). The only optional
addition is a machine-readable form (e.g. `mst --version --json`) so `setup.sh`
can parse robustly; the human form suffices today. `mst` gains **no** self-update
code, no `upgrade` flag, no update config.

## Dev builds & missing tools

- **`dev` builds** (`mst dev`): a skill's range check treats `dev` as
  *unsatisfiable* unless the range explicitly allows it — so a developer running
  a local `mst dev` won't silently satisfy a skill expecting `v0.4+`; setup.sh
  installs a real release into the cache instead. (Developers using `go install`
  for direct `mst` use are a separate audience and unaffected.)
- **Missing tool:** setup.sh installs it; the skill never assumes a PATH binary.

## Decisions

- **Nested `metadata.requires` map** (tool → range), not flat `requires_<tool>`
  keys — scales as a skill gains dependencies and reads cleanly.
- **Pinned version, not range-resolution:** `setup.sh` installs an exact tested
  version (`VERSION` literal); a new upstream release never auto-replaces the
  skill's known-good binary. The pin moves only when the skill is re-released
  after re-testing.
- **Range syntax:** bare `>=X,<Y` strings, awk-compared in `scripts/setup.sh`
  for the **runtime drift-check** (is the installed version still compatible?).
  Not used to select which release to download.
- **Single canonical cache path** — no duplicate copy inside the skill dir, even
  for harnesses (like pi) that resolve relative paths. One source of truth.
- **No `mst` self-update** — the skill is the unit of upgrade; `mst` gains no
  updater code.

## Deferred

- **Promote the `agent-skills/` cache convention upstream** to the
  agentskills.io standard / document it for third-party skills — **collect
  real-world experience with it first**, then propose.
