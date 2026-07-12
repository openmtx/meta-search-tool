# Skill Dependencies & Versioning

How `mst`'s Agent Skill declares and manages the CLI tools it drives (`mst`,
`webclaw`).

## Principle

The **skill owns its tools** — `scripts/setup.sh` is the single source of truth
for what versions of `mst` and `webclaw` the skill needs. It pins an **exact
tested version** and SHA256-verifies every download. A new upstream release
never auto-replaces a skill's known-good binary.

## Install target: `~/.local/bin/`

Tools are installed to `${HOME}/.local/bin/` (the [XDG `$XDG_BIN_HOME`
default](https://wiki.archlinux.org/title/XDG_Base_Directory#User_directories)),
which is on `PATH` for most modern systems. `setup.sh` warns at install time if
it is not.

## Frontmatter — declaring tool versions

The Agent Skills standard frontmatter has no native version/dependency field, so
declarations go in the standard **`metadata:`** map:

```yaml
---
name: web-search
metadata:
  version: "1.0.0"                 # this skill's own version
  requires:                        # nested: tool → semver range
    mst: ">=0.3,<1.0"
    webclaw: ">=0.6,<1.0"
---
```

`metadata.requires.<tool>` is the **compatibility range** (documentation +
runtime drift-check). The actual version selector lives in `scripts/setup.sh`
(literal pin), not in frontmatter.

## `scripts/setup.sh` — find-or-install

Contract:

1. **Find:** is `<install-dir>/<tool>` present, executable, and the pinned
   version (`<tool> --version`)? If yes → fast no-op.
2. **Drift-check:** if present but a different version, verify it still
   satisfies `requires.<tool>`; otherwise reinstall the pinned version.
3. **Install:** download the **pinned** release over HTTPS, **SHA256-verify**
   against the release's checksum file, and atomically place it (temp file →
   `mv` onto final path; mode `0755`).
4. **Report:** print the bin dir to stdout; on any failure, one clear line +
   non-zero exit.

## Runtime range check

When the skill runs, its setup confirms the installed tool still satisfies the
declared range — catching drift if the user manually swapped the binary. On
mismatch: clear stderr line + remediation hint, never a silent break.

## Dev builds

`dev` builds are treated as *unsatisfiable* by the range check — so a developer
running a local `mst dev` won't silently replace a skill-pinned release.
`setup.sh` installs a real release instead.
