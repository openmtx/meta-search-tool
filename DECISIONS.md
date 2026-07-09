# DECISIONS.md

Log of implementation decisions that are **not in** `docs/SPEC.md` or that **go
against** it: ambiguities resolved, workarounds, open questions, and proposed
spec changes. `docs/SPEC.md` is the source of truth; this file is the exception
log. When a decision here is accepted, fold it into the spec and leave the entry
as a dated record. See `AGENTS.md`.

## 2026-07-09 — default engine setup aligned to SearXNG (§5/§6/§17.3)
- **Context:** User requested researching SearXNG's default engine state and
  matching it. Reviewed `ref/searxng/searx/settings.yml` — SearXNG ships
  `mojeek` and `hackernews` as `disabled: true`. Our tool had them enabled
  by default (mojeek as a keyless general-web fallback; hackernews for tech
  news). This is a spec deviation: the SPEC and code previously treated both
  as default-on.
- **Decision:** Disabled **mojeek** and **hackernews** by default, matching
  SearXNG's defaults. The default-on set is now:
  - Enabled: `wikipedia`, `arxiv`, `pubmed`, `stackexchange`, `brave`
    (auto-skips if no key), `ddg` (keyless general-web).
  - Disabled (SearXNG compat): `mojeek`, `hackernews`, `grokipedia`,
    `google`, `bing`.
  - Disabled (own default — keyed/paid API, Tier D): `tavily`.
  Users can enable with `-e mojeek`, `-e hackernews`, or config `enabled: true`.
- **Rationale:** Matching the reference metasearch implementation's defaults
  reduces surprise for users familiar with SearXNG. Both engines remain fully
  functional — only their default-enabled state changed. ddg is the sole
  keyless general-web engine on by default (verified reliable via the SearXNG-
  mechanism redesign). Note: `tavily` is not a SearXNG engine; its
  disabled-by-default status is our own design choice (keyed, paid API, Tier D)
  and not part of the SearXNG alignment.
- **Status:** accepted — folded into spec §5 (engine tables, notes, access),
  §6 (config example), §16 (roadmap), §17.3 (default-on set), §18 (deferred),
  §19 (profiles), engine descriptors, and their tests.

## 2026-07-09 — tavily answer field: separate envelope field, NOT composed with RRF
- **Context:** §18 open question: "AI-oriented answer/summary output (§5 Tier D):
  Tavily's answer mode returns a synthesized summary beyond the URL list — decide
  whether such an 'answer' composes with RRF at all, or belongs in a separate
  output field." Sprint D.1.2 added `include_answer` and `search_depth` support.
- **Decision:** The `answer` field is a **separate top-level field** in the output
  envelope (`Envelope.Answer`). It does NOT compose with RRF — the summarized
  answer is not a search result and should not be ranked, scored, or deduplicated.
  It is surfaced in the `--format json` merged output envelope when: (a) tavily
  is configured with `include_answer: true` and (b) the API returns a non-empty
  `answer`. The field is `omitempty` so it's absent when not populated. Under
  `--raw`, the answer is already part of the native tavily payload. Under
  `--no-merge`/`jsonl`/`text`/`table`, the answer is not included (the answer
  concept is tied to the merged JSON view; `--raw` is available for full fidelity).
- **Rationale:** The answer is a synthesized summary, not a ranked URL result.
  Mixing it into the RRF pipeline would distort scores and confuse consumers that
  expect uniform result objects with rank/score/source. A separate field lets
  downstream harnesses treat it as metadata rather than a search result.
- **Status:** accepted — folded into spec §5 (Tier D note), §10 (envelope schema),
  TASKS.md Sprint D.1.2, and implemented in code.

## 2026-07-09 — grokipedia stability assessment
- **Context:** grokipedia was shipped with `enabled: false` per SearXNG's
  `inactive` marking (experimental). The live test (`MST_ITEST=1`) returned 10
  results reliably on the first run, with clean HTML-stripped snippets and
  correct `/page/<slug>` URLs. However, grokipedia is a newer/less-established
  service (launched ~2025) compared to arxiv/pubmed/hackernews which have
  decades of uptime.
- **Decision:** Keep `enabled: false` for now. grokipedia is functional and the
  API is clean, but it's still experimental upstream. If live testing over a
  longer period shows consistent availability, promote to `enabled: true` and
  update this entry.
- **Rationale:** Conservative — don't promote an experimental engine until we
  have confidence in its uptime. The `MST_ITEST=1` gate ensures it's tested
  regularly.
- **Status:** accepted — grokipedia stays `enabled: false`.


## 2026-07-09 — Google public CSE token path (SearXNG-style, keyless, merged into hybrid engine)
- **Context:** User asked to add SearXNG's `google_cse` approach (keyless, uses
  blackle.com's public CSE ID) as a mode in the existing `google` engine rather
  than a separate engine. Compared two options:
  - **Option A** (chosen): merge as a third mode in the hybrid `google` engine;
    precedence: official API → public CSE (SearXNG-style, built-in CX) → HTML
    scrape. A `google` is a `google`.
  - **Option B** (rejected): separate `google_cse` engine (matching SearXNG's
    architecture but diverging from mst's design where a search-domain gets one
    engine).
- **Decision:** Added triple-mode dispatch to `google`:
  1. **Official REST API** when `GOOGLE_API_KEY` + `GOOGLE_CX` both set.
  2. **Public CSE token** (default, keyless) when a CX is available from env var
     or the built-in default (`partner-pub-8993703457585266:4862972284`,
     blackle.com's public CSE, same as SearXNG). Fetches a 1-hour-cached session
     token from `google.com/cse/cse.js?cx=<CX>`, then queries
     `cse.google.com/cse/element/v1` with the token.
  3. **HTML scrape** (fallback) when no CX is available at all.
  The engine descriptor remains `Keyed: false` (works keyless via public CSE).
  Default CX is package-var settable for tests via `SetDefaultPublicCX("")`.
  SearXNG-style `cse_token` caching (1h TTL, `sync.Mutex`-guarded) implemented
  with `ResetCSETokenCache()` for deterministic tests.
- **Rationale:** User preference for merged engine ("a google is a google");
  the public CSE path is strictly better than HTML scraping (structured results,
  fewer bot blocks, same keyless UX as SearXNG). The blackle.com CSE is a public
  Programmable Search Engine — no API key needed, and SearXNG has relied on it
  since 2021. HTML scrape is now a tertiary fallback-of-last-resort.
- **Status:** accepted — spec §5 (Tier C table, access notes), §6 (config
  example note), §17.3 (disabled-by-default reasoning updated). Engine code,
  tests (CSE httptest round-trip, golden-table parse, token cache, error paths,
  SearchWithRaw), and scrape-mode tests (with `scrapeOnly` helper to clear
  default CX) all implemented and green.

<!--
Entry template:

## YYYY-MM-DD — <short title>
- **Context:** …
- **Decision / question:** …
- **Rationale:** …
- **Status:** proposed | accepted (folded into spec §X) | rejected
-->

## 2026-07-09 — arxiv AND-term query: REVERTED (match SearXNG all:<q>)
- **Context:** Initially fixed arxiv's OR-flooding by AND-ing terms. However,
  checking SearXNG's `arxiv.py` revealed it uses the same standard
  `search_query=all:<q>` pattern — our AND fix was a divergence from the
  reference implementation.
- **Decision:** **Reverted the AND fix.** arxiv now uses `all:<q>` like SearXNG.
  The OR-flooding is acceptable because:
  1. arXiv is a science/academic engine — `all:<q>` OR-matching is standard API
     usage (published as `search_query=all:<raw>` in the API docs).
  2. Sprint B.3.1 (engine categories) will scope arxiv to `--engine academic`,
     preventing it from running on general-web queries entirely.
  3. The category fix is architectural; altering query semantics would be a
     permanent divergence from the reference implementation.
- **Rationale:** Match SearXNG's standard `all:<q>` pattern; categories (not
  query hacks) are the correct fix for engine-scoping problems.
- **Status:** accepted — `buildRequestURL` reverted to `all:`+q; arxiv test
  wantQ values reverted; live test retains in-domain assertion but removed
  out-of-domain ≤5 check. Sprint B.3.1 tracks the categories fix.

## 2026-07-09 — ddg enabled by default (overturns §17.3 "off by default")
- **Context:** §17.3/§5/§6 shipped `ddg` as `enabled: false` on the rationale
  that DDG's scrape is "fragile" and "datacenter requests get heavily
  anti-scraped." Live testing this session overturned that premise: after the
  SearXNG-mechanism redesign + UA rotation, ddg returned 10 results reliably on
  every live run, while mojeek (the other keyless general-web engine) got HTTP
  403-blocked mid-query. A default-flags query ("Erling Haaland diet") showed the
  default set has no robust general-web fallback when mojeek is blocked — ddg
  would have answered it but was off.
- **Decision:** Flip `ddg` Descriptor `Enabled` to true. ddg joins mojeek as the
  second keyless general-web engine on by default; disable with `--exclude ddg`
  or `ddg.enabled: false`. Weight stays 1.0 (set earlier). grokipedia remains
  `enabled: false` (still experimental/inactive upstream).
- **Rationale:** The original fragility rationale no longer matches observed
  reality; ddg is now the more reliable of the two keyless general-web engines.
  Two general-web engines on by default gives a keyless install redundancy.
- **Status:** accepted — folded into spec §5 (Mojeek note, Tier C table, DDG
  note), §6 (config example enabled:true), §16 (roadmap), §17.3. Code + tests
  updated; `TestRun_DDG_OffByDefault` repurposed to `TestRun_DDG_OnByDefault`.

## 2026-07-09 — live-only engine bugs: pubmed/hackernews/stackexchange; ddg weight 1.0
- **Context:** An all-engines live run of a real query surfaced that 3
  "complete" engines (httptest-green) failed against their live endpoints —
  the same live-fidelity gap that hit ddg/grokipedia. hackernews/pubmed/stackexchange
  all used the `base.Path = "..."` pattern on an apiBase that already carried a
  path prefix; `url.URL.Path` assignment *replaces* (not appends), dropping the
  prefix and hitting the wrong path. Their httptest tests asserted the dropped
  path, so passed while broken (identical to the grokipedia bug found earlier).
- **Root cause (recurring):** 4 engines (grokipedia, hackernews, pubmed,
  stackexchange) shared the `apiBase="<host>/<prefix>"` + `base.Path="/<leaf>"`
  anti-pattern. Fixed uniformly: apiBase = host origin; base.Path = the full path.
  Tests now assert the full path; apiBase is overridden to the host only.
- **Per-engine fixes (all live-verified via new MST_ITEST=1 tests):**
  - **hackernews:** `/search` → `/api/v1/search` (was 404).
  - **pubmed:** `/esearch.fcgi`,`/esummary.fcgi` → `/entrez/eutils/...` (was 404).
    esearch kept XML (dropped the contradictory `retmode=json`; the live XML
    `<eSearchResult><IdList><Id>` already matched the struct). esummary switched
    XML→JSON: its struct expected `<ArticleSet><Article><title>` which never
    matched live (`<eSummaryResult><DocSum><Item Name="Title">…>`); the JSON
    `retmode=json` schema `{result:{uids:[],<pmid>:{title,source,pubdate}}}` is
    far cleaner.
  - **stackexchange:** versionless `/search/advanced` + invalid hardcoded
    `filter=!*236cL9uK5*(T6` → 400. Fixed to `/2.3/search/advanced`, valid
    `filter=withbody` (SE shorthand that includes body for snippets), and
    `maxcount`→`pagesize` (maxcount isn't a search/advanced param).
- **ddg weight 0.5→1.0:** a live run showed ddg (weight 0.5) found the most
  relevant results but ranked below a flood of high-count arxiv/grokipedia hits;
  bumped to 1.0 so web-search results compete. Spec §6 config example updated.
- **Status:** accepted — engine + test fixes folded in; 4 engines now carry
  MST_ITEST=1 live tests (pubmed/hackernews/stackexchange added; ddg/grokipedia
  already had them). ddg weight folded into spec §6. No further spec change
  needed (the access-note endpoints were already correct; only the code diverged).

## 2026-07-09 — grokipedia review fixes (path bug + html→text + live test)
- **Context:** Sprint B.1.5 (`grokipedia`) was implemented by a concurrent
  session and marked complete, but a review against spec §5 + SearXNG
  `engines/grokipedia.py` found two issues, one masked by the engine's own tests.
- **Findings & fixes:**
  1. **Wrong API path (critical, masked by tests).** `buildRequestURL` set
     `base.Path = "/full-text-search"` on `apiBase = "https://grokipedia.com/api"`;
     assigning `url.Path` *replaces* (not appends), dropping `/api` → produced
     `https://grokipedia.com/full-text-search` instead of `…/api/full-text-search`
     (spec §5 + SearXNG). The httptest + buildRequestURL tests asserted the wrong
     path, so they passed while the code was broken (would 404 live). Fixed by
     making `apiBase` the host origin and `base.Path` the full `/api/full-text-search`
     (the author's intended base+path design, corrected); tests fixed to assert
     `/api/full-text-search`. Verified empirically + via the live test.
  2. **Missing html→text on snippet (spec deviation).** Spec §5 says "(html→text
     on snippet)" and SearXNG applies `html_to_text(item["snippet"])`; the impl
     only `TrimSpace`d → HTML would leak into snippets. Added a `<[^>]*>` tag
     stripper (same approach as the stackexchange engine) + a golden test.
  3. **No live test (de-risker).** Added gated `TestSearch_Live` (MST_ITEST=1,
     §14) — the gate that would have caught #1. Verified green: 10 real results,
     correct `/page/<slug>` URLs (slug `Go!_(…)` → `Go%21_%28…%29`), clean snippets.
- **Other (accepted as-is):** `offset` param intentionally omitted (mst applies
  offset post-merge per §9, like single-page ddg); `limit` capped at 100
  (unverified the API honors >10 — SearXNG always sends 10 + paginates; the live
  test sends limit=10 so this stays untested, low risk for an off-by-default
  engine); weight 1.0 (undocumented but reasonable for a disabled engine);
  retry coverage thinner than peers (only 429-succeeds + exhausted).
- **Status:** accepted — folded into the engine + tests; TASKS.md B.1.5 block
  de-garbled. No spec change needed (impl now matches §5).

## 2026-07-09 — ddg UA rotation (pulls forward §18)
- **Context:** §18 listed "scrape-UA rotation" as deferred, and the 2026-07-09
  ddg redesign used a single static browser UA. The user requested rotating
  "spoofed" browser UAs (like a browsing user) picked at random.
- **Decision:** Implement UA rotation now. `ddg` picks one realistic browser UA
  per `Search()` call, uniformly at random from `browserUAs` (a pool of common
  browser/OS combinations), so requests distribute across DDG's per-UA
  reputation buckets rather than pinning one identity. The pick is **per query**
  (not per HTTP request) so a future `vqd` multi-page sequence stays valid
  (`vqd` is derived from query+UA; docs/DDG-research.md). Page-1-only today, so
  cross-request stability isn't currently required. `SetUASelector` provides a
  deterministic test hook.
- **Rationale:** Rotation avoids a single throttleable/fingerprintable UA
  identity while still presenting a realistic browser to DDG's bot blocker.
- **Status:** accepted — folded into spec §12 (carve-out now "rotated at random
  from a pool"), §5 (ddg note + access notes), §18 (scrape-UA rotation no longer
  deferred; only `MST_USER_AGENT` user-configurability remains deferred).

## 2026-07-09 — DDG redesigned to SearXNG mechanism; §12 scrape-UA carve-out
- **Context:** §12 mandated an honest `mst/<version>` User-Agent for *all*
  engines, and §5's DDG note said "`mst` sends only an identifying `User-Agent`".
  But the A.2.1 `ddg` implementation **already violated §12** — it sent a
  spoofed Chrome UA plus full browser headers, with no logged decision. Research
  (`docs/DDG-research.md`, distilled from SearXNG `engines/duckduckgo.py`)
  confirmed *why*: DDG's no-JS html endpoint bot-blocks identifying/non-browser
  UAs (returns an anomaly/CAPTCHA page with zero results) and requires a static
  browser UA + `Sec-Fetch-*`/`Referer` headers + form POST. The honest-UA policy
  and a functional DDG scrape are mutually exclusive. The prior
  "DDG real-world selector/URL fidelity unverified" entry (2026-07-08) flagged
  the guessed selectors and missing live test as the gate before promoting DDG.
- **Decision:** User chose "make it work" via the SearXNG mechanism.
  1. **§12 carve-out:** scrape engines may send a static browser UA + browser
     `Sec-Fetch-*`/`Referer` headers; structured-API engines keep
     `mst/<version>`. Scoped precisely: `ddg` uses the browser UA (verified);
     `mojeek` keeps the honest UA for now (scrape-tolerant, unchanged).
  2. **`ddg` reimplemented (SearXNG-faithful):** form **POST** to
     `html.duckduckgo.com/html/` with body `q=&b=&kl=wt-wt` (page 1), static
     browser UA + `Sec-Fetch-Dest: document` / `Sec-Fetch-Mode: navigate` /
     `Sec-Fetch-Site: same-origin` / `Sec-Fetch-User: ?1` / `Referer`.
     Selectors corrected to the reverse-engineered ones: container
     `#links > div[class~=web-result]`, url+title `h2 > a`, snippet
     `a.result__snippet`. CAPTCHA (`<form id="challenge-form">`) is detected
     post-fetch and surfaced as `ErrCaptcha` (not silent zero results, and not
     retried — retrying won't clear it and may worsen IP reputation). Queries
     ≥500 chars are skipped (DDG rejects them). The `uddg=` redirect is unwrapped.
     **Single page only** — the `vqd` token required for page 2+ (extracted from
     the page-1 response, cached per query+UA) is a documented follow-up; a
     single page yields ~10–15 results, which the ranker handles gracefully.
  3. **`MST_ITEST=1` live test added** (`TestSearch_Live`, §14) and **verified
     green**: 10 real results, selectors confirmed against live markup, URLs
     clean/unwrapped. This resolves the 2026-07-08 "fidelity unverified" entry.
     The full binary path also verified (`mst --engine ddg "go concurrency"` →
     10 ranked results, exit 0).
- **Rationale:** Honest UA makes DDG non-functional; browser simulation is the
  established approach (SearXNG). Surfacing CAPTCHA as an error turns silent
  failures into transparent ones. The live test is the real de-risking gate the
  prior decision required.
- **Status:** accepted — folded into spec §12 (scrape-UA carve-out), §5 (DDG
  note + access notes), §18 (UA deferral wording). Resolves the 2026-07-08
  "DDG fidelity unverified" entry (now marked resolved below).

## 2026-07-08 — HN Algolia hits without URL use objectID fallback
- **Context:** The HN Algolia API returns `url` for most hits, but some (especially
Show HN posts or deleted items) may have an empty `url`. Each hit always has
an `objectID` which corresponds to the HN story/item ID.
- **Decision:** When `url` is empty and `objectID` is non-empty, construct the
URL as `https://news.ycombinator.com/item?id=<objectID>`. If both are empty,
skip the hit.
- **Rationale:** Ensures every valid HN hit produces a usable result URL even
when Algolia doesn't include a direct link. The HN item page is always
accessible via this convention.
- **Status:** accepted — implemented in Sprint B.1.4.

## 2026-07-08 — Stack Exchange body strips HTML tags
- **Context:** The Stack Exchange API returns the `body` field as raw HTML
(e.g., `<p>Goroutines are lightweight threads managed by the Go runtime.</p>`).
Other engines (arxiv, pubmed) return plain text in their metadata fields.
- **Decision:** Use a simple regex-based HTML tag stripper (`<[^>]*>`) in
`stripNewlines` to produce clean plain-text snippets. This is sufficient for
the snippet field — full HTML-to-text conversion is deferred.
- **Rationale:** The snippet is consumed by an LLM or displayed in text output;
raw HTML tags add noise without adding value. A simple regex strip is fast,
stateless, and handles the common tag patterns in Stack Exchange answers.
- **Status:** accepted — implemented in Sprint B.1.3.

## 2026-07-08 — PubMed esummary uses lowercase element names
- **Context:** The NCBI E-utilities esummary endpoint returns XML with lowercase
element names (e.g., `<title>` not `<Title>`, `<source>` not `<Source>`), unlike
the arXiv Atom feed which uses PascalCase. This is a documented NCBI convention.
- **Decision:** Use lowercase XML tags in the Go structs (`xml:"title"`,
`xml:"source"`, `xml:"pubdate"`) to match PubMed's actual response format.
The esearch endpoint uses uppercase `<IdList>`, `<Id>` (matching arXiv style).
- **Rationale:** PubMed's API is well-documented and stable; matching the exact
element casing ensures correct deserialization.
- **Status:** accepted — implemented in Sprint B.1.2.

## 2026-07-08 — arXiv engine uses Atom XML (not JSON)
- **Context:** The spec §5 lists `arxiv` as a "free API" for CS/physics/math
  papers. Unlike `wikipedia` and other Tier B engines that return JSON, the arXiv
  API returns Atom XML (`export.arxiv.org/api/query`). The spec's access notes
  (§5) say "JSON-API engines" include arXiv but the actual API is Atom XML.
- **Decision:** Parse as Atom XML using Go's `encoding/xml`. Map `<id>` → URL
  (`https://arxiv.org/abs/<id>`), `<title>` → title, `<summary>` → snippet. The
  `--raw` path returns the decoded XML structure. This is consistent with how
  other XML-returning APIs (e.g., PubMed) will be handled.
- **Rationale:** arXiv's API is well-documented and stable; Atom XML parsing is
  straightforward with Go's stdlib. The `--raw` path still works — it just
  returns an XML-decoded structure rather than a JSON map.
- **Status:** accepted — implemented in Sprint B.1.1.

## 2026-07-07 — Config discovery uses MST_CONFIG_HOME, not XDG_CONFIG_HOME
- **Context:** SPEC §6 specified config discovery as
  `$XDG_CONFIG_HOME/mst/config.yaml` (fallback `~/.config/mst/config.yaml`).
  `mst` is a single self-contained CLI with no desktop-environment coupling, so
  reading a generic XDG variable is unjustified — and it clashes with the spec's
  own rule that `mst`'s control variables use the `MST_` prefix (§6).
- **Decision:** Replace the `XDG_CONFIG_HOME` override with an `mst`-owned
  `MST_CONFIG_HOME`. It points **directly at the mst config directory** (not a
  base dir), so the file is `$MST_CONFIG_HOME/config.yaml` — mirroring
  `DOCKER_CONFIG`. When unset/empty, fall back to `~/.config/mst/config.yaml`
  (unchanged). A missing file is still not an error; only an existing-but-
  unreadable/malformed file is (exit `2`).
- **Rationale:** Self-contained tool; no XDG dependency; consistent with the
  `MST_` env-var prefix the spec already mandates.
- **Status:** accepted — folded into spec §6.

## 2026-07-07 — Short flags (-e/-n/-f/-q, -v/-vv) need a non-stdlib parser
- **Context:** SPEC §4 documents short flags `-e`, `-n`, `-f`, `-q` (aliases of
  `--engine`, `--limit`, `--format`, `--query`) and `-v`/`-vv` (repeatable
  count-based verbosity). The A.0.1 implementation used Go's stdlib `flag`
  package, which cannot express short/long aliases or repeatable count flags, so
  none of these work (e.g. `mst -e brave q` → "flag provided but not defined:
  -e"). This is a deviation from §4 surfaced by the A.0.1 review.
- **Decision / question:** **Accepted — swap to `github.com/spf13/pflag`.** pflag
  supports short+long aliases (`-e`/`--engine`, `-n`/`--limit`, `-f`/`--format`)
  and repeatable count flags (`CountVarP` → `-v`/`-vv`), which stdlib `flag`
  cannot. The custom `--weight` `flag.Value` is replaced by pflag's
  `StringArrayVarP` (each occurrence appends, no comma splitting). `--help`
  returns `pflag.ErrHelp`, surfaced as `cli.ErrHelp` so `main` exits 0.
- **Rationale:** stdlib `flag` cannot represent the documented CLI surface;
  pflag is the standard, pure-Go (statically linked, no runtime dep) solution.
- **Status:** accepted — implemented; code now conforms to spec §4. §3 updated to
  name pflag. (No §4 change needed — the flags were already specified.)

## 2026-07-07 — Sprint A.0.2 scope clarifications & deferrals
- **Context:** Sprint A.0.2's DoD is the merged path: dispatcher (§7), RRF + dedup
  + sort (§9), formatters json/jsonl/text (§10), structured stderr errors + exit
  codes 0/1/2/3 (§4, §11). Several documented flags/modes are v1-scope but fall
  outside this sprint's DoD, and a few spec areas are silent. Logging them up
  front so nothing is silently dropped or silently invented.
- **(a) `source` is derived centrally by the ranker, not read from `Result.Source`.**
  §8's `Result` has a `Source` field ("registered domain (eTLD+1)") and §10/§17.13
  say `source` is "extracted via a public-suffix-aware parser". Decision: the
  ranker derives `source` from each merged entry's displayed URL via
  `golang.org/x/net/publicsuffix.EffectiveTLDPlusOne`, deterministically — it does
  NOT trust per-engine `Result.Source`. `Result.Source` stays on the struct for
  future `--raw`/native output and for engines that choose to populate it, but the
  merged JSON's `source` is always derived. Rationale: deterministic (every engine
  hitting the same canonical host yields the same source), DRY (no per-engine
  publicsuffix wiring), and matches §10's "extracted via parser" wording. This is
  a clarification of §8/§10, not a contradiction.
  **Status:** accepted (clarification; no spec change needed — consistent with §10/§17.13).
- **(b) Unknown engine names in `--engine`/`--exclude`/`--require` → exit 2.** §4
  explicitly makes an unknown name in `--weight` a usage error (exit 2) and lists
  valid names in the message. It is silent on unknown names in the other
  engine-name flags. Decision: extend the same fail-fast to `--engine`, `--exclude`,
  and `--require` — an engine name not in the registry is exit 2 with the valid
  names listed. Rationale: consistency with the documented `--weight` behavior;
  fail-fast on a typo beats silently ignoring it.
  **Status:** accepted — folded into spec §4 ("Engine selection precedence").
- **(c) `--no-merge` and `--raw` deferred from A.0.2 to A.2.x (hardening).** Both
  are v1-scope (§16) but are tier-3 debug modes and are NOT in the A.0.2 DoD.
  `--raw` additionally cannot be meaningfully exercised until real engines expose
  native payloads (Stage 1). Decision: in A.0.2 these flags surface a clear exit-2
  "not yet implemented" error (not a silent no-op) and land fully in A.2.x.
  Rationale: keeps A.0.2 to its DoD (the merged path) while never silently
  ignoring a documented flag. (`--raw --format text|table|jsonl` is already exit 2
  per §10; only the `--raw --format json` path is deferred.)
  **Status:** accepted — `--no-merge` implemented in A.2.2 (skip ranking; emit
  per-engine native-order groups with `engine` field, no score/rank). `--raw`
  implemented in A.2.2 (per-engine raw payloads via `RawEngine` interface +
  `DispatchRaw`; all 4 engines implement it).

## 2026-07-08 — `--no-merge` and `-q`/`--query` implemented
- **Context:** DECISIONS (c) deferred `--no-merge` and `--raw` to A.2.x; the
  `-q`/`--query` flag was documented in §4 but not yet reflected here as an
  accepted decision. Both are now shipped in Sprint A.2.2.
- **Decision (`--no-merge`):** Implemented per §10 — skip ranking/dedup, emit
  results grouped per-engine in native order with an `engine` field, no
  `score`/`rank`. Works with json/jsonl/text. `--offset` is a no-op (with `-v`
  notice). Composition: `--raw --no-merge` accepted but is a no-op (`--raw`
  implies `--no-merge`).
- **Decision (`-q`/`--query`):** Registered as a `StringVarP` flag. Query can
  come from `-q`/`--query`, positional args, or `--stdin` — but only one source
  at a time (mutual exclusion enforced). Conflicts → exit 2.
- **Rationale:** Both raise the bar for "taggable" v0.1; `--no-merge` is a
  clean in-scope feature, `-q` closes the last Tier 1 deviation from §4.
- **Status:** accepted — both implemented in A.2.2; no spec change needed
  (conforms to §4/§10).
- **(d) `--format table` deferred to v0.2 (§16).** Decision: in v0.1 `table`
  surfaces a clear exit-2 "planned for v0.2" error. Spec-conforming (§16 schedules
  `table` for v0.2).
  **Status:** accepted (spec-conforming).
- **(e) Full `--list-engines` output deferred to A.2.2.** A.0.2 keeps a minimal
  placeholder (prints registered engine names). The full §4 output (name/enabled/
  keyed/key_present/weight/status, respecting `--format`) lands in A.2.2
  ("hardening: --list-engines …"), which is where TASKS.md already schedules it.
  **Status:** accepted (in-plan deferral).
- **(f) JSON output is compact (not pretty-printed).** §10's `json` example is
  indented, but that is illustrative (no normative "MUST indent" statement).
  Decision: emit compact JSON for both `json` and `jsonl` (jsonl is one object per
  line by definition). Rationale: pipeline-friendly (`mst … | jq …`), smaller on
  the wire/context, consistent with jsonl, and jq/agents parse it identically. The
  field SHAPE matches §10 exactly; only whitespace differs.
  **Status:** accepted (presentation detail; spec example was illustrative).
- **(g) `context.DeadlineExceeded` is reported as the string `"timeout"`.** §11
  says a per-engine deadline firing is "reported as `timeout`", but Go's raw error
  string is "context deadline exceeded". Decision: the `engines` block `error`
  field normalizes `context.DeadlineExceeded` (and only that) to `"timeout"`; all
  other errors use `err.Error()` verbatim.
  **Status:** accepted (matches §11 wording).

## 2026-07-08 — Wikipedia OpenSearch API response type

- **Context:** The Wikipedia OpenSearch API returns a heterogeneous JSON array: `[query_string, [titles_array], [descriptions_array], [urls_array]]`. Go's `json.Unmarshal` cannot directly decode this into a single typed struct without a custom UnmarshalJSON.
- **Decision:** Use `[]json.RawMessage` for the response type. This lets us unmarshal the raw JSON once, then selectively unmarshal elements 1-3 as `[]string` (the title/description/URL arrays). Element 0 is the query string (ignored by mst).
- **Rationale:** Simpler than a custom UnmarshalJSON; zero-copy for the inner arrays; works cleanly with httptest fixtures.
- **Status:** accepted — implemented in `internal/engine/wikipedia/wikipedia.go`.

## 2026-07-08 — Centralized retry/backoff in `internal/engine/retry` (§11)

- **Context:** Sprints A.1.1 (brave) and A.1.2 (mojeek) each carried a verbatim
  copy of the same retry loop + helpers (`isRetryable`, `calcBackoff`,
  `applyRetryAfter`, `retryable`, `sleepCtx`, `jitter`). The REVIEW.md follow-up
  flagged this DRY violation (P2) and, on closer inspection during the refactor,
  the duplicated loops contained three latent bugs: (a) a **double-sleep** — the
  HTTP-error path called `retryable()` (which itself sleeps) and then `sleepCtx()`
  again, so every HTTP-error retry slept twice (~3s per exhausted-retry test
  instead of ~1.5s); (b) a **connection leak** — `defer resp.Body.Close()` inside
  the pagination loop held N bodies open until `Search()` returned; (c) a wrong
  `Retry-After` semantics — `min(Retry-After, backoff)` silently ignored the
  server's requested wait whenever it exceeded the calculated backoff.
- **Decision:** Extract a single `internal/engine/retry` package with a `Do(ctx,
  engineName, newReq)` that encapsulates the whole retry loop (§11) and returns a
  2xx response or an error. Every HTTP engine calls `retry.Do` instead of
  inlining its own loop. Three behaviors are fixed in the move:
  1. Single sleep per retry (the loop calls `Sleep` exactly once per attempt).
  2. The successful response body is closed by the caller immediately after
     parsing (no `defer` inside the loop).
  3. `ApplyRetryAfter` uses `max(Retry-After, backoff)` — never retry faster than
     the backoff, but honor a server-requested longer wait; the per-engine context
     deadline is the real cap (§11).
- **Rationale:** One correct, tested implementation shared by all engines; the
  bugs were invisible precisely because they were duplicated. Centralizing also
  makes the retry policy a single grep target for the spec-against-code check.
- **Status:** accepted — folded into spec §11 (retry policy wording +
  `internal/engine/retry` reference) and §15 (layout).

## 2026-07-08 — Paginating engines return partial results on mid-pagination failure (§7)

- **Context:** §7 stated "A failing/timed-out engine returns `(nil, error)`".
  Mojeek paginates (up to 10 pages). If page N succeeds but page N+1 exhausts
  retries, the A.1.2 code returned `(nil, error)`, discarding the pages already
  fetched (REVIEW.md P1 bug). The ranker already fuses `run.Results` regardless of
  `run.Err`, and the `engines` block reports `ok:false` with the partial `count` —
  so partial results + error flow through the pipeline correctly.
- **Decision:** A paginating engine that fails mid-way returns its accumulated
  results *plus* the error: `(allResults, lastErr)`. Single-request engines still
  return `(nil, error)` on failure. The dispatcher/ranker/format/exit-code path
  already handles non-nil error with non-empty results (ranker fuses the partial
  set; `engines` block shows `ok:false` + partial count; `--strict`/`--require`
  still surface the failure as exit 3).
- **Rationale:** Discarding fetched pages is strictly worse; the partial set is
  real, ranked data and the failure is still transparently reported.
- **Status:** accepted — folded into spec §7 (clarified the `(nil, error)`
  contract for paginating engines).

## 2026-07-08 — DDG real-world selector/URL fidelity unverified (§5 Tier C)

- **Context:** Sprint A.2.1 shipped the `ddg` HTML-scrape engine against
  synthetic httptest fixtures. The §5 access notes give explicit selectors for
  `mojeek` but are silent on DDG's exact HTML structure, and there is no
  `MST_ITEST=1` live test (§14 lists it as optional; none exists in-repo). Two
  real-world concerns surfaced in the A.2.1 review:
  1. The golden-table fixture nests `h2.result__title`/`p.result__snippet`
     *inside* `a.result__a` — the inverse of the live `html.duckduckgo.com/html/`
     markup (`h2.result__title > a.result__a`; snippet as `a.result__snippet`).
     The class-only descendant selectors (`div.result`, `a.result__a`,
     `h2.result__title`, `.result__snippet`) are robust to both shapes, so
     parsing *probably* works — but the test is near-tautological and does not
     prove live correctness.
  2. Live DDG wraps result URLs in `//duckduckgo.com/l/?uddg=<encoded>`
     redirect links. `ddg.parseResults` does no redirect unwrapping, so a
     wrapped href would flow into the ranker's canonicalizer / `source`
     derivation unchanged, yielding a `duckduckgo.com` `source` (and a canonical
     URL under `duckduckgo.com`) for every result.
- **Decision / question:** Treat `ddg` as **unvalidated against the live site**
  for v0.1. It is already off by default (§17.3) and labeled "best-effort,
  fragile" (§5), so this does not block v0.1. **Before any release that
  promotes `ddg` toward default-on or documents it for routine use: add an
  `MST_ITEST=1` live test (§14) and (a) confirm the selectors against real
  markup, (b) add `uddg=`/`/l/` redirect unwrapping if present** — or update the
  spec §5 DDG note with the confirmed selectors. No code change now.
- **Rationale:** §5 explicitly accepts DDG's fragility; shipping an opt-in
  scrape engine without a live integration test is consistent with the spec.
  Logging it here so it is tracked when DDG's reliability is next evaluated
  (§18 lists `ddg`/`mojeek` scrape viability under monitoring).
- **Status:** resolved 2026-07-09 — `ddg` reimplemented via the SearXNG
  mechanism with the confirmed selectors (`#links > div[class~=web-result]`,
  `h2 > a`, `a.result__snippet`), `uddg=` unwrapping, CAPTCHA detection, and an
  `MST_ITEST=1` live test that returned 10 real results. See the 2026-07-09
  DDG-redesign entry above; spec §5 access notes now carry the confirmed
  selectors. (Originally proposed 2026-07-08: risk recorded; no spec change yet.)

## 2026-07-08 — `-q`/`--query` flag implemented (spec deviation resolved)

- **Context:** Spec §4 flag table documents `-q, --query` as a flag alias for the
  positional query argument, but the implementation only accepted the query as
  positional args — a spec deviation flagged in the Tier 1 code review.
- **Decision:** Register `-q`/`--query` as a `StringVarP` flag in the Tier 1 flag
  set. The query can come from `-q`/`--query`, positional args, or `--stdin` — but
  only one source at a time: `--query` + positional, `--query` + `--stdin`, and
  `--stdin` + positional are all usage errors (exit 2). The `Flags.QueryFromFlag`
  field tracks whether the flag was used (vs positional), and `--help` now lists
  `--query` as the first Tier 1 flag. The `helpTiers` comment about "not a
  registered flag" is removed.
- **Rationale:** Conformance to the spec table; closes the only Tier 1 deviation.
- **Status:** accepted — folded into spec §4 (the flag table was already correct;
  the implementation now matches).

## 2026-07-08 — `--list-engines` full output + remaining v0.1-scope items

- **Context:** Sprint A.2.2 ("hardening"). DECISIONS (e) deferred the full
  `--list-engines` output (§4) to this sprint, and DECISIONS (c) assigns
  `--no-merge`/`--raw` to "A.2.x (hardening)". §16 lists `--raw`, `--no-merge`,
  and "skill-tier flag classification" as v0.1 scope. The A.2.2 DoD enumerates
  "fuzz/property tests, `--list-engines`, `--format text/table`, exit codes" but
  also says "v0.1 is taggable" — so the boundary of what blocks a v0.1 tag is
  ambiguous.
- **Decision (part 1 — `--list-engines`, implemented):**
  - Output shape per §4: each entry is `name`, `enabled` (config/flag state),
    `keyed`, `key_present` (a real bool for keyed engines, JSON `null` for
    keyless), `weight` (effective, after config + flag merge), and `status` ∈
    {`ready`,`disabled`,`missing-key`}. Renders via `format.MarshalListEngines`
    (json array / jsonl one-per-line / text aligned table; `table` → exit 2 as
    v0.2). `--list-engines` short-circuits before query resolution and exits 0.
  - State resolution is shared with `resolvePlan` via a new `computeStates`
    (resolve.go), so precedence (§6) and engine selection (--engine/--exclude)
    are identical to a normal run.
  - `--list-engines` performs **no** engine-name validation (unknown names in
    `--engine`/`--exclude` do not cause exit 2). Rationale: §4 says it "exits 0
    without searching" — it is an introspection command and should not hard-fail
    on a typo; the run-time `--engine`/`--exclude`/`--require` exit-2 validation
    still guards actual searches.
- **Decision (part 2 — remaining v0.1 items, resolved):** `--no-merge` (§10)
  and `--raw` (§10) implemented in A.2.2; `-q`/`--query` (§4) registered.
  `--help` tier grouping implemented in A.2.2. All are now shipped — no
  remaining A.2.2 blockers.
- **Status:** resolved — all items implemented in A.2.2; no spec change needed.

## 2026-07-09 — default search scope: 'general' category (§4)
- **Context:** User requested making the `general` category/profile the default
  for searching. Previously, no `--engine` flag meant all enabled engines ran.
  This was inconsistent with the v1.0 scope ("general web search only") and
  the planned search profiles feature (§19).
- **Decision:** When no `--engine` is specified, the search scope is limited to
  the `general` category (`brave`, `ddg`; see category table in §5). Use
  `--engine all` to run every enabled engine, or any explicit `--engine` to
  select a different set. The category filter is applied in `computeStates`
  (resolve.go) as a default scope, between the config layer and the explicit
  whitelist layer in the enabled-computation precedence chain: default → config
  → **category default** → --engine whitelist → --exclude. An explicit --engine
  overrides the category default entirely.
- **Rationale:** Aligns the default scope with v1.0's "general web search only"
  scope. The `general` category (brave + ddg) covers the most common use case
  (general web search) while keeping the tool lightweight by default — no
  reference/academic/news engines run unless explicitly requested. This is a
  natural precursor to the planned `--profile` feature (§19).
- **Spec changes:** §4 — `--engine` default changed from `*all enabled*` to
  `*general*`; engine selection precedence amended to describe the category
  default; §19 — "General search stays the no-op default" updated to reflect
  the `general` category default. §17.3 — default-on set description updated.
- **Code changes:** `computeStates` in `cmd/mst/resolve.go` — added
  `useGeneralOnly` flag that filters to `category == "general"` when no
  `--engine` whitelist is present. `fakeEngine.Descriptor()` in
  `cmd/mst/main_test.go` — defaults empty category to `"general"` for test fakes.
  Two tests that relied on non-general engines by default (`TestRun_Raw_ImpliesNoMerge`,
  `TestRun_Wikipedia_E2E`) updated to pass `-e wikipedia`.
- **Status:** accepted — code and spec updated.

## 2026-07-09 — google enabled by default in general category (§5)
- **Context:** After making the `general` category the default search scope, the
  user requested enabling `google` in the general profile, positioned after `ddg`
  in registration order (which it already is — ddg at 9, google at 10 in
  `defaultRegistry()`). Google has three search modes (official API → public CSE
  token → HTML scrape). The public CSE token mode is keyless via the built-in CX
  (blackle.com's public CSE), so google works out of the box without any env vars.
- **Decision:** Toggled `google`'s `Enabled` from `false` to `true` in its
  `Descriptor()`. This deviates from SearXNG compat (SearXNG has google as
  `inactive`/disabled) but is justified because the public CSE token mode works
  reliably and keylessly, and the user explicitly wants it in the default scope.
- **Spec changes:** §5 category table — `general` updated: `brave, ddg` →
  `brave, ddg, google`. §5 Tier C table — google note: added "On by default."
  §6 config example — `enabled: false` → `enabled: true`. §17.3 — default-on
  set: google moved from "disabled" to "enabled", with a note that this is an
  exception to SearXNG compat.
- **Code changes:** `google.Descriptor()` Enabled: `false` → `true`
  (`internal/engine/google/google.go`). `TestDescriptor` updated to expect
  `Enabled == true` (`internal/engine/google/google_test.go`).
- **Status:** accepted — code and spec updated.
