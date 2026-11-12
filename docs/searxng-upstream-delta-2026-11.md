# SearXNG upstream delta — 2026-08-03 → 2026-11-12

Research date: **2026-11-12**
Purpose: track what changed in the reference metasearch implementation since
`mst`'s last commit, and decide what matters for a **single-binary, LLM-first**
meta-search tool.

## Scope and method

| Item | Value |
|---|---|
| `mst` baseline | `e80996c` — 2026-08-03 18:29 UTC (`E.1.9i: generate .a import lib via dlltool on Windows`) |
| Upstream base | `aa059419` — 2026-08-03 19:17 +0200 (last `searxng/searxng` commit before the `mst` baseline timestamp) |
| Upstream head | `de722ab1` — 2026-11-12 09:20 +0100 (`[mod] CI - set SEARXNG_CONTACT_URL=...`) |
| Commits merged in window | **230** |
| Diff size | **504 files**, **+55,924 / −25,818** |

Method: full-history partial clone of `searxng/searxng`, `git log`/`git diff`
from `aa059419..master`, programmatic comparison of both `searx/settings.yml`
versions (PyYAML), source review of every engine `mst` ships, review of new
engines, and live smoke tests with the repository's `./mst` binary.

> This document is research only. No spec, engine, or task file was changed.
> Anything below that implies a behavior change needs a SPEC update and/or a
> `DECISIONS.md` entry, per `AGENTS.md`.

---

## 1. Executive summary

1. **The keyless-scrape arms race got materially worse.** SearXNG now marks
   **Mojeek `inactive`** because it serves an **Altcha proof-of-work CAPTCHA**
   (`searx/settings.yml:2844-2866`), and its network layer moved from `httpx`
   to **`curl_cffi`** so requests can impersonate a browser's TLS fingerprint
   (`be836e614`, 2026-09-03). Its DuckDuckGo JSON path also gained a
   JavaScript-challenge solver (`765a9999d`, 2026-09-09).
2. **`mst`'s keyless general-web coverage is currently fragile in practice.**
   Live checks from this machine on 2026-11-12:
   - `ddg` (default-on) → **CAPTCHA challenge** (3/3 runs); `mst` correctly
     reports it.
   - `mojeek` (opt-in, disabled by default) → Altcha CAPTCHA page, parsed as
     **`ok:true, count:0`** (silent zero-result failure — no error surfaced).
   - `google` (public CSE path, default-on) → **works** (20 results).
3. **Google's HTML path was replaced upstream.** SearXNG now requests
   `https://www.google.com/wml/search` with a **Nokia feature-phone User-Agent**
   and `sca_esv=1` (`searx/engines/google.py:298-329`, commit `a4cb7df05`).
   `mst`'s HTML-scrape fallback still uses `www.google.com/search` + a Chrome
   UA (`internal/engine/google/google.go:434-535`); its public-CSE mode is the
   path that still works and should stay primary.
4. **Upstream defaults changed around the general category.** `google` moved
   `inactive → disabled` (HTML engine maintained but off), `mojeek` moved
   `disabled → inactive`, while **`duckduckgo`, `brave`, `google cse`,
   `wikipedia`, and `wikidata` remain on by default**. `google cse` is now the
   supported keyless Google path.
5. **New engines relevant to `mst`:** **`jina`** (keyed AI search, LLM-oriented,
   inactive by default), **`europepmc`** (keyless academic REST API, on by
   default), and **`yandex_api`** (keyed paid general API, inactive). Most other
   additions (`readerfunded`, `xprivo`, `findborg`, `littlelayer`,
   `searchrockit`, `s1search_rampjs`, `ayo`) are inactive/experimental and not
   worth following now.
6. **LLM-relevant result handling improved upstream:** titles are capped at
   **200 chars**, content at **1200 chars**, whitespace-normalized, and
   duplicate title/content removed (`searx/result_types/_base.py:38-39,120-121`,
   `42829cecd`). `mst` only truncates the `table` format; JSON/JSONL remain
   uncapped.
7. **Upstream did not touch the things that differentiate `mst`:** ranking,
   RRF/weights, per-engine score transparency, CLI, or JSON output. The
   ranking-control niche remains unoccupied.
8. **No engine source changes for `arxiv`, `pubmed`, `stackexchange`, or
   `hackernews`** in this window. All four passed live smoke tests.

---

## 2. Default/status delta for engines `mst` ships

Statuses from upstream `searx/settings.yml` (base → head). In SearXNG,
`disabled` means user-disabled by default; `inactive` means upstream considers
the engine not currently usable (e.g. CAPTCHA/broken).

| Engine | Base | Head | Effect / note |
|---|---|---|---|
| `ddg` | on | on | HTML POST mechanism unchanged; JSON variant hardened |
| `brave` | on | on | API fixes only; HTML parser rewritten (not our path) |
| `google` | `inactive: true` | `disabled: true` | HTML path now WML/Nokia; off by default |
| `google cse` | on | on | Remains the keyless Google path; `enable_http3` added |
| `wikipedia` | on | on | New contactable-UA requirement; source changed |
| `wikidata` | on | on | Large internal refactor (not shipped by `mst`) |
| `arxiv` | on | on | **No source diff** |
| `pubmed` | on | on | **No source diff** |
| `stackexchange` | on | on | **No source diff** |
| `hackernews` | disabled | disabled | **No source diff**; matches our default |
| `grokipedia` | disabled + inactive | disabled + inactive | Unchanged; matches our default |
| `bing` | disabled | disabled | **No source diff**; our keyed Bing is unaffected |
| `mojeek` | `disabled: true` | `inactive: true` | Altcha PoW CAPTCHA; upstream stopped shipping it usable |

`mst` descriptors live in each engine's `Descriptor()` (e.g.
`internal/engine/mojeek/mojeek.go:55-66`,
`internal/engine/google/google.go:100-113`).

---

## 3. Engine-by-engine changes that matter

### 3.1 Mojeek — now an Altcha CAPTCHA, upstream `inactive` (highest impact)

- **Upstream:** `3accbaca7` (`[feat] mojeek: automatically solve altcha
  captchas`, 2026-09-11) and settings `searx/settings.yml:2844-2866`.
  The engine fetches `/captcha/challenge`, solves an Altcha proof-of-work
  (`solve_altcha`), posts the solution to `/captcha/verify`, and stores the
  `chllg` cookie (`searx/engines/mojeek.py:81-101`).
- **`mst`:** `internal/engine/mojeek/mojeek.go:28` sends `mst/dev` and
  `parseResults` (`:199-220`) only looks for `ul.results-standard > li`.
  A CAPTCHA page therefore parses to **zero results with no error**.
- **Evidence:** `./mst -e mojeek -n 2 --timeout 15s 'Go programming language'`
  returned `{"mojeek":{"ok":true,"count":0,"error":""}}`; a direct `curl` of
  `https://www.mojeek.com/search?q=golang&safe=0` returned a 5.3 KB page whose
  `<title>` is `Captcha` and which contains `altcha`/`challenge` markers —
  with both `mst/dev` and a Chrome UA.
- **Spec mismatch:** SPEC §5 calls Mojeek "scrape-tolerant" and §12 keeps the
  honest UA because it is "disabled by default". That assumption no longer holds.
- **Options:**
  1. **Detect** the Altcha/CAPTCHA page and return a typed error (same contract
     as `ddg.ErrCaptcha`) instead of `ok:0` — required either way.
  2. Mark `mojeek` `Enabled: false` with an explicit "upstream inactive /
     Altcha" note in SPEC §5/§17.3.
  3. Implement Altcha solving (Go port of the PoW; non-trivial, and upstream
     itself says it "requires much computer power") or use Mojeek's paid API.
  4. Remove Mojeek from the default/scrape story until (3) is justified.
- **Recommendation:** do (1) + (2) now; treat (3) as a separate spike only if
  Mojeek coverage is a product requirement.

### 3.2 DuckDuckGo — mechanism unchanged, bot detection hardened elsewhere

- **Upstream:** `duckduckgo.py` (the HTML POST + `vqd` engine `mst` follows) has
  only minor changes in the window:
  - `62366b05c` — ignore zero-click answers that have no URL.
  - `274b63b67` — use `resp.html()` internally.
  - `4e2c1ea7f` — preload-link timeout uses global config (this is in
    `duckduckgo_web.py`, not the HTML engine).
- **The bigger DDG change is a different engine:** `duckduckgo_web`
  (`765a9999d`) added `_solve_jsa` (`searx/engines/duckduckgo_web.py:99-131`),
  which detects DDG's `let jsa = ...` JavaScript challenge and follows the
  computed `links.duckduckgo.com` URL, plus Firefox TLS impersonation
  (`impersonate="firefox"`, `default_headers=False`). This engine is still
  **disabled by default**.
- **`mst`:** still implements the upstream HTML POST mechanism
  (`internal/engine/ddg/ddg.go:1-36`), with vqd multi-page and CAPTCHA detection.
  Live checks got CAPTCHA 3/3 from this IP. That is environmental/anti-bot
  behavior, not proof of a code regression — upstream's HTML engine is still
  enabled and uses the same mechanism.
- **Difference worth testing:** upstream uses a **static** browser UA per
  process (`_HTTP_User_Agent = gen_useragent()`), because `vqd` is query+UA
  bound; `mst` rotates UA per query (`internal/engine/ddg/ddg.go:62-79`).
  Rotating across queries is plausible, but if failures persist, try a static
  per-process UA and the exact upstream headers.
- **Recommendation:** keep the CAPTCHA error path; add a live-test gate that
  runs from CI and records per-IP behavior. Do **not** port `duckduckgo_web`'s
  JS solver without TLS impersonation — Go's stdlib TLS fingerprint is likely
  one of the signals DDG checks.

### 3.3 Google — HTML path is now WML + Nokia UA; CSE is the reliable path

- **Upstream:** `a4cb7df05` (`[fix] google: use Nokia UA`) replaced the
  JS-laden HTML scrape with a **WML** request:
  - URL: `https://www.google.com/wml/search?...&sca_esv=1`
    (`searx/engines/google.py:328`)
  - UA: random Nokia feature-phone string (`:71-78`, `:329`)
  - TLS impersonation: `chrome99_android` (`:330`)
  - The old `.g` / `/url?q=` parser and `arc_id` async logic were deleted.
  - `response.results` is now `XML`, not `HTML` (`:49`).
- **`mst`:** triple-mode is:
  1. official API (`fetchAPI`),
  2. public CSE token (`fetchPublicCSE`, `google.go:309+`),
  3. HTML scrape (`fetchScrape`, `google.go:434-535`).
  Live test used mode 2 and returned 20 results, so the keyless path is healthy.
  Mode 3 is stale relative to upstream and its Chrome UA/`/search` endpoint is
  the least likely to survive.
- **CSE changes upstream:** `enable_http3 = True`, `gl` country handling, and
  removal of `lr`/`cr` args (`searx/engines/google_cse.py`); no structural break
  for our token request.
- **Recommendation:** document CSE as the primary keyless mode (already true in
  code); either (a) port the WML/Nokia request as the fallback, accepting that
  it may need TLS impersonation, or (b) retire the scrape fallback and keep
  CSE/API only. Update SPEC §5 access notes either way.

### 3.4 Brave — API fixes, not our code path

Upstream's `braveapi` (paid API, same family as `mst`'s `brave`) received
several fixes:

| Commit | Change | `mst` status |
|---|---|---|
| `745d5b6fc` | pagination sent an invalid `offset` | `mst` does not paginate Brave (limit capped at 20) |
| `3e454637f` | set `Accept: application/json` | already set (`internal/engine/brave/brave.go:99`) |
| `cccbc9e15` | set `Cache-Control: no-cache` | **not set** — cheap to add |
| `a9d990033` | correct SafeSearch level mapping | `mst` has no SafeSearch option yet |
| `adbf160ff` | don't parse web results as news | `mst` only parses `web` |

The large `brave.py` rewrite (`d47cb96bf`, `c0014f015`) is for the **HTML**
Brave engine (SvelteKit IIFE data extraction), not the API; irrelevant while
`mst` stays keyed-API. It is a warning that a keyless Brave port would be a
large, brittle project.

### 3.5 Wikipedia — upstream now requires a contactable User-Agent

- **Upstream:** `38929d148` and `deab13e21`:
  - `wikipedia.py` requires `general.contact_url`; `setup()` returns false
    (engine disabled) when it is unset (`searx/engines/wikipedia.py:138-158`).
  - Requests set `User-Agent: searxng_useragent(<contact>)` and
    `impersonate="none"` (`:172`).
  - The reason stated in the commit: comply with the policy of **Wikidata and
    other services**; contact information is required.
- **`mst`:** `internal/engine/wikipedia/wikipedia.go:31` sends `mst/dev` with
  no contact URL. It works today (live test returned 9 results), but this is
  exactly the kind of UA Wikimedia is starting to reject.
- **Recommendation:** make the API-engine UA contactable, e.g.
  `mst/<version> (+https://github.com/openmtx/meta-search-tool)` as the
  supported default (SPEC §12 currently specifies `mst/<version>`; extending it
  with a URL is a small, defensible spec update). Add an optional
  `MST_CONTACT_URL`/config override if desired.

### 3.6 Unchanged engines

- `arxiv.py`, `pubmed.py`, `stackexchange.py`, `hackernews.py`: **zero source
  diff** in the window. Live tests all green on 2026-11-12.
- `grokipedia`: settings unchanged (`disabled: true`, `inactive: true`); `mst`
  already ships it disabled. Live test green.
- `bing`: unchanged upstream (disabled). `mst` uses the keyed Bing Web Search
  v7 API, so upstream's scrape engine is not a dependency.

---

## 4. New engines: which are worth following

| Engine | Upstream commit | Access | Upstream default | What it is | Fit for `mst` |
|---|---|---|---|---|---|
| **`jina`** | `0a118066d` (2026-08-05) | keyed (`JINA_API_KEY`-style) | inactive | `s.jina.ai` AI search; modes `reader`/`google`/`bing`; `respondWith=no-content`; JSON (`searx/engines/jina.py`) | **Good Tier D candidate** — directly LLM-oriented, same shape as `tavily` |
| **`europepmc`** | `ba055b3e0` (2026-09-02) | keyless | **on** | Europe PMC REST (`ebi.ac.uk/europepmc/...`); JSON; cursor pagination; `Paper` metadata (journal, DOI, authors, OA PDF) (`searx/engines/europepmc.py`) | **Good Tier B candidate** — complements `pubmed`, covers preprints/OA |
| `yandex_api` | `ef9a188cc` (2026-08-13) | keyed, paid | inactive | Official Yandex Cloud Search API v2; base64 XML (`searx/engines/yandex_api.py`) | Optional Tier C keyed engine; not urgent |
| `readerfunded` | `d138f443b` (2026-11-02) | keyless (key optional) | inactive | Independent index, JSON API, 120 req/min free (`searx/engines/readerfunded.py`) | Watch; inactive upstream, unproven |
| `xprivo` | `783a09462` (2026-09-03) | placeholder API key | inactive | SSE/JSON, general/news/videos | Skip |
| `findborg` | `d63bbef7a` (2026-09-10) | keyless | inactive | Brave proxy | Skip (we have Brave API) |
| `littlelayer` | `74ffd0115` (2026-09-03) | keyless | inactive | Indie JSON index | Skip for now |
| `searchrockit` | `aef258321` (2026-09-04) | keyless | inactive | Google-derived HTML | Skip |
| `s1search_rampjs` | `15b0c8ef3` (2026-09-03) | keyless | inactive | JS-heavy HTML | Skip |
| `ayo` | `74418092d` (2026-11-01) | keyless | inactive | Anubis CAPTCHA | Skip |

Removed upstream: `adobe_stock`, `cara`, `heexy`, `loc` (Library of Congress),
`searx_engine` — none are shipped by `mst`. `andisearch` was added and removed
inside the window (`2a6fc07be` → `81371861f`), showing how fast the scraped
long tail churns.

---

## 5. Architecture / network changes

### 5.1 `httpx` → `curl_cffi` (browser TLS impersonation)

- `be836e614` (`[mod] network: migrate to curl_cffi`, 2026-09-03) replaces the
  HTTP client with `curl_cffi`, default impersonation `chrome`, and per-engine
  `impersonate`/`default_headers`/`curl_options` (`searx/network/client.py`,
  `searx/search/processors/online.py:88-93`).
- This is a major reason the scrape hardening works: modern anti-bot systems
  commonly fingerprint **TLS/JA3**, not just headers, so a browser UA alone is
  often not enough.
- **For `mst`:** this is a strategic fork in the road.
  - Porting `curl_cffi` would introduce a C dependency and break the
    "single static binary, no runtime deps" property.
  - The Go-native option is `refraction-networking/utls` (custom ClientHello)
    plus HTTP/2/3 support — more code and binary size, but no C.
  - The cheapest path is **API-first**: structured APIs don't need
    impersonation. That is already `mst`'s Tier A/B design.
- Recommendation: do not chase TLS impersonation until scrape coverage becomes
  an explicit product goal. If it does, prototype `utls` behind a build tag and
  measure binary size.

### 5.2 Result model and LLM-context handling

- `42829cecd` adds `MAXIMUM_TITLE_LENGTH_CHARS = 200` and
  `MAXIMUM_CONTENT_LENGTH_CHARS = 1200`, normalizes whitespace, and truncates at
  word boundaries with `" …"` (`searx/result_types/_base.py:38-39,120-121`).
- `e40de2d99` adds a `Video` result type; `europepmc` introduces `Paper`
  (`searx/result_types/paper.py:34`) with journal/DOI/authors/OA-PDF fields.
- `mst`'s `engine.Result` is intentionally flat (`internal/engine/engine.go:25-30`)
  and only `table` truncates (`internal/format/table.go:38-54`).
- **Recommendation:** cap JSON/JSONL title/snippet lengths (200/1200 is a good
  starting point) to keep agent context bounded. Keep it configurable or at
  least documented. Richer academic metadata is optional; it would require a
  `Result` schema change (spec §8/§10) and should wait for a real consumer.

### 5.3 Smaller changes (mostly web-app-only)

- `SXNG_Response.html()` helper (`3169aafd2`/`274b63b67`) — internal.
- `setup()` replacing `init()` for simple engines (`b4f616fed`) — internal.
- `search.method` default `POST → GET`, autocomplete default
  `none → duckduckgo` (`1c3bb1e88`) — browser UX; no CLI impact.
- New category `stock_images` and category moves (`280aceb1a`, `1f7711ef1`) —
  not relevant to `mst`'s web/academic/reference categories.
- Security fixes in the calculator, preferences zlib cap, proxy trust
  (`5ffd32ca2`, `5f4005996`, `487d7a96e`) — web-app only; no `mst` exposure.
- Container/CI/release/l10n/static/dependency updates dominate the remaining
  diff (115 translation files, 36 static, 25 simple-client, etc.); out of scope.

---

## 6. Live smoke test (2026-11-12, `./mst` at `e80996c`)

Commands used a single engine, `-n 2`, `--timeout 15s`; outputs trimmed.

| Engine | Result | Detail |
|---|---|---|
| `wikipedia` | ✅ | 9 results, `ok:true` |
| `arxiv` | ✅ | 20 results |
| `pubmed` | ✅ | 1 result |
| `stackexchange` | ✅ | 15 results |
| `hackernews` | ✅ | 20 results |
| `grokipedia` | ✅ | 20 results |
| `google` | ✅ | 20 results via public CSE |
| `ddg` | ❌ | `ddg: CAPTCHA challenge (bot detection triggered)` — 3/3 runs |
| `mojeek` | ⚠️ | `ok:true, count:0`; direct request returned Altcha `<title>Captcha</title>` |

No code/build/test changes were made for this research, so the Go test suite was
not re-run. The `./mst` binary matches `HEAD` (`git status` only has untracked
Python build artifacts).

---

## 7. Recommendations, prioritized

### P0 — correctness / trust (LLM tool must not silently lie)

1. **Mojeek: detect Altcha and surface an error.** Add a CAPTCHA/challenge check
   in `internal/engine/mojeek/mojeek.go` before parsing, returning a typed
   error (mirror `ddg.ErrCaptcha`). This turns a silent `ok:0` into an honest
   engine failure.
2. **Update the Mojeek contract in SPEC §5/§12/§17.3.** The "scrape-tolerant,
   honest UA" assumption is stale; upstream now marks Mojeek `inactive` because
   of Altcha. Log the decision in `DECISIONS.md` before changing code/spec.
3. **Keep DDG's CAPTCHA error path and watch it.** Do not change DDG code based
   on one IP; re-test from CI/another network. If failures persist, try a static
   per-process UA and upstream's exact headers before considering a JS solver.

### P1 — low-risk upstream alignment

4. **Wikipedia/API UA:** append a contactable project URL to the API-engine UA
   (`mst/<version> (+https://github.com/openmtx/meta-search-tool)`), or add a
   contact/config override. This follows upstream's contactable-UA policy and
   costs one constant + tests.
5. **Brave:** add `Cache-Control: no-cache` to requests; no other API change is
   needed for `mst`'s current capabilities.
6. **Google:** make CSE the documented primary keyless mode; either port the
   WML/Nokia fallback or remove the stale HTML scrape. Update SPEC §5 access
   notes to match whichever path we keep.
7. **Add `europepmc`** (keyless academic) if broader literature coverage is
   wanted. It is default-on upstream, has a stable REST API, and maps cleanly to
   `engine.Result` (title/url/snippet). Requires SPEC §5 + descriptor + tests.
8. **Add `jina`** (keyed AI search) to the Tier D roadmap if AI answer/search
   engines remain a priority. It is the closest upstream analogue to `tavily`.

### P2 — LLM ergonomics / strategic

9. **Cap JSON/JSONL result text** (title 200, snippet/content 1200) so an LLM
   gets bounded context from every format, not just `table`.
10. **Decide on default-engine alignment.** Upstream's default general set is
    now `brave + ddg + google cse + wikipedia + wikidata`. `mst` deliberately
    ships a broader default (including arxiv/pubmed/stackexchange) and has no
    `wikidata` engine. This is a product decision, not a bug; do not change it
    without a SPEC §17.3 update.
11. **TLS-impersonation study (only if scrape is strategic).** Prototype
    `utls` behind a build tag, measure binary size, and compare against the
    API-first path. Do not add `curl_cffi` (C dependency) to a single-binary
    tool.

---

## 8. Open questions for approval

1. Should Mojeek be **detected + disabled/inactive** (recommended), or should we
   invest in Altcha solving / the paid API?
2. Should `mst` adopt a **contactable UA** for Wikipedia/all API engines, and
   what contact URL should it use?
3. Is **`europepmc`** wanted in Tier B, and **`jina`** in Tier D? Both are spec
   changes (§5, §6, §17.3) and need tests.
4. Should JSON/JSONL adopt upstream's **200/1200 character caps**, or stay
   lossless and leave truncation to the caller?
5. Should we align the default engine set with upstream, or keep `mst`'s broader
   default?

No changes were made to `docs/SPEC.md`, engine code, `TASKS.md`, or
`DECISIONS.md` as part of this research.
