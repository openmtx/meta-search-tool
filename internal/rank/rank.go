// Package rank implements result merging and ranking: URL canonicalization for
// deduplication (§9), weighted Reciprocal Rank Fusion scoring, deterministic
// dedup/merge, the final sort, and output windowing. Every function here is pure
// (no concurrency, no I/O), which makes them ideal golden-table and
// property/fuzz-test targets (§14).
package rank

import (
	"math"
	"net"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/openmtx/meta-search-tool/internal/engine"
	"golang.org/x/net/publicsuffix"
)

// DefaultK is the canonical RRF smoothing constant (Cormack, Clarke & Büttner),
// the value used across most RRF deployments (§9).
const DefaultK = 60

// minFetchFloor is the minimum per-engine fetch cap (§9: max((offset+limit)*3, 20)).
const minFetchFloor = 20

// trackingParams is the conservative default set of tracking query params stripped
// during canonicalization (§9 step 3.5). `ref` is deliberately excluded (too many
// legitimate params share that name).
var trackingParams = map[string]bool{
	"utm_source":   true,
	"utm_medium":   true,
	"utm_campaign": true,
	"utm_content":  true,
	"utm_term":     true,
	"gclid":        true,
	"fbclid":       true,
	"msclkid":      true,
	"mc_cid":       true,
	"mc_eid":       true,
}

// wwwLabelRe matches a leading host label of `www` with optional trailing digits
// (www, www2, www3, …) — dropped during canonicalization (§9 step 3.6).
var wwwLabelRe = regexp.MustCompile(`^www\d*$`)

// Canonicalize normalizes a URL for deduplication equivalence by applying the
// §9 step-3 sub-steps IN ORDER:
//  1. lowercase scheme + host;
//  2. drop default ports (:443 for https, :80 for http);
//  3. remove the fragment;
//  4. strip a trailing slash (preserving a single `/` on bare roots);
//  5. strip tracking query params (preserving the order and original encoding
//     of the remaining params — no reordering, no percent re-encoding);
//  6. drop a leading `www[ digits]` host label.
//
// Percent-encoding and mobile/regional host aliases are intentionally NOT
// normalized in v1 (§18). A URL that cannot be parsed or has no host is returned
// lowercased + trimmed (best-effort grouping — real engines always send hosts).
func Canonicalize(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return strings.ToLower(strings.TrimSpace(raw))
	}

	// 1. lowercase scheme + host.
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)

	// 2. drop default ports.
	host, port := u.Hostname(), u.Port()
	if (u.Scheme == "https" && port == "443") || (u.Scheme == "http" && port == "80") {
		port = ""
	}
	if port != "" {
		u.Host = net.JoinHostPort(host, port)
	} else {
		u.Host = host
	}

	// 3. remove fragment.
	u.Fragment = ""
	u.RawFragment = ""

	// 4. strip trailing slash (preserve single `/` on bare roots).
	if u.Path == "" {
		u.Path = "/"
	}
	u.Path = strings.TrimRight(u.Path, "/")
	if u.Path == "" {
		u.Path = "/"
	}

	// 5. strip tracking query params (preserve order + original encoding).
	u.RawQuery = stripTracking(u.RawQuery)

	// 6. drop leading www[ digits] host label.
	u.Host = dropWWW(u.Host)

	return u.String()
}

// stripTracking removes tracking query params from a raw query string while
// preserving the order and original percent-encoding of the survivors (Go's
// url.Values.Encode would reorder alphabetically and re-encode — both forbidden
// by §18's "percent-encoding is not normalized in v1").
func stripTracking(rawQuery string) string {
	if rawQuery == "" {
		return ""
	}
	parts := strings.Split(rawQuery, "&")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			continue
		}
		key := p
		if i := strings.IndexByte(p, '='); i >= 0 {
			key = p[:i]
		}
		if trackingParams[strings.ToLower(key)] {
			continue
		}
		out = append(out, p)
	}
	return strings.Join(out, "&")
}

// dropWWW drops a leading www[ digits] label from the host portion of a
// host[:port] string (§9 step 3.6). A single-label host is preserved.
func dropWWW(hostport string) string {
	host, port, err := net.SplitHostPort(hostport)
	if err != nil {
		host = hostport
		port = ""
	}
	labels := strings.Split(host, ".")
	if len(labels) > 1 && wwwLabelRe.MatchString(labels[0]) {
		labels = labels[1:]
	}
	host = strings.Join(labels, ".")
	if port != "" {
		return net.JoinHostPort(host, port)
	}
	return host
}

// SourceFromURL returns the registered domain (eTLD+1) of a URL, extracted via the
// maintained public-suffix list (§17.13): en.wikipedia.org → wikipedia.org,
// news.bbc.co.uk → bbc.co.uk. On any parse/publicsuffix failure it falls back to
// the bare hostname (or "" if there is none). The merged output's `source` is
// always derived this way — deterministically, from the displayed URL (§10).
func SourceFromURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	host := u.Hostname()
	if reg, err := publicsuffix.EffectiveTLDPlusOne(host); err == nil {
		return reg
	}
	return host
}

// RankedResult is one merged, scored result emitted by the ranker. It is the
// JSON-serializable unit of the merged output (§10): rank (1-indexed, assigned
// after windowing), the carried fields (taken from the highest-weighted
// contributing result — the §9 snippet rule), the aggregate weighted RRF score,
// and the contributing engine names ordered by descending weighted contribution
// then registration name. The unexported fields are deterministic sort keys used
// by the final sort (§9 step 4) and are not serialized.
type RankedResult struct {
	Rank    int      `json:"rank"`
	Title   string   `json:"title"`
	URL     string   `json:"url"`
	Source  string   `json:"source"`
	Snippet string   `json:"snippet,omitempty"`
	Score   float64  `json:"score"`
	Engines []string `json:"engines"`

	canonical string // canonical URL — final-sort tie-break (d)
	topReg    int    // registration order of the highest-contributing engine — tie-break (c)
	numEng    int    // number of contributing engines — tie-break (b)
}

// Rank fuses per-engine runs into a single merged, deduplicated, scored, sorted
// list (§9). It is a pure function: identical inputs always yield an identical
// ordering, independent of goroutine scheduling (the output never depends on
// which engine finished first). regOrder maps each engine name to its
// registration index; weights maps each engine name to its effective weight; k is
// the RRF smoothing constant (use DefaultK / 60 when in doubt). The returned
// slice is fully sorted but NOT windowed and NOT rank-numbered — call Window to
// apply --offset/--limit and assign 1-indexed ranks.
func Rank(runs []engine.EngineRun, weights map[string]float64, regOrder map[string]int, k int) []RankedResult {
	if k <= 0 {
		k = DefaultK
	}

	type agg struct {
		score      float64            // sum of weighted contributions across engines
		contrib    map[string]float64 // engine name -> its total weighted contribution
		bestWeight float64            // single highest weighted contribution seen
		bestReg    int                // registration order of best (snippet/title carrier)
		bestResult engine.Result      // the result carrying title/url/snippet
	}

	aggs := map[string]*agg{} // canonical URL -> agg

	for _, run := range runs {
		name := run.Engine
		w := lookupWeight(weights, name)
		ro := lookupReg(regOrder, name)
		for r, res := range run.Results {
			canon := Canonicalize(res.URL)
			a, ok := aggs[canon]
			if !ok {
				a = &agg{contrib: make(map[string]float64), bestReg: math.MaxInt}
				aggs[canon] = a
			}
			c := w * (1.0 / float64(r+k)) // §9 step 1+2: score then weight
			a.score += c
			a.contrib[name] += c
			// §9 snippet/title selection: highest single weighted contribution;
			// ties broken by registration order (first registered wins).
			if c > a.bestWeight || (c == a.bestWeight && ro < a.bestReg) {
				a.bestWeight = c
				a.bestReg = ro
				a.bestResult = res
			}
		}
	}

	out := make([]RankedResult, 0, len(aggs))
	for canon, a := range aggs {
		// engines[] ordered by descending weighted contribution, then registration.
		engines := make([]string, 0, len(a.contrib))
		for name := range a.contrib {
			engines = append(engines, name)
		}
		sort.Slice(engines, func(i, j int) bool {
			ci, cj := a.contrib[engines[i]], a.contrib[engines[j]]
			if ci != cj {
				return ci > cj
			}
			return lookupReg(regOrder, engines[i]) < lookupReg(regOrder, engines[j])
		})

		title := NormalizeTitle(a.bestResult.Title)
		out = append(out, RankedResult{
			Title:     title,
			URL:       a.bestResult.URL,
			Source:    SourceFromURL(a.bestResult.URL),
			Snippet:   NormalizeSnippet(title, a.bestResult.Snippet),
			Score:     a.score,
			Engines:   engines,
			canonical: canon,
			topReg:    a.bestReg,
			numEng:    len(engines),
		})
	}

	// §9 step 4: deterministic final sort (see sortRanked).
	sortRanked(out)
	return out
}

// sortRanked orders merged results per §9 step 4, all tie-breakers deterministic:
//  1. score descending;
//  2. number of contributing engines descending;
//  3. registration order of the highest-contributing engine ascending;
//  4. canonical URL ascending (a total-order safety net so output never
//     depends on map iteration or goroutine scheduling).
func sortRanked(out []RankedResult) {
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		if a.numEng != b.numEng {
			return a.numEng > b.numEng
		}
		if a.topReg != b.topReg {
			return a.topReg < b.topReg
		}
		return a.canonical < b.canonical
	})
}

// Window applies --offset/--limit to a ranked list and assigns 1-indexed ranks
// (§9 step 5): skip the first `offset` results, take the next `limit`, then number
// the survivors 1..N. A non-positive limit means "no cap" (take all remaining).
// The returned slice shares the input's backing array; Window mutates Rank fields
// in place.
func Window(results []RankedResult, offset, limit int) []RankedResult {
	if offset < 0 {
		offset = 0
	}
	if offset > len(results) {
		offset = len(results)
	}
	results = results[offset:]
	if limit > 0 && limit < len(results) {
		results = results[:limit]
	}
	for i := range results {
		results[i].Rank = i + 1
	}
	return results
}

// FetchLimit computes the per-engine FETCH cap (§9): max((offset+limit)*3, 20).
// This is NOT the user's --limit (which bounds only the final merged output) —
// engines fetch deep native rank lists so RRF has rank depth to fuse. Growing the
// fetch with offset keeps deep pages fed (§4).
func FetchLimit(offset, limit int) int {
	n := (offset + limit) * 3
	if n < minFetchFloor {
		return minFetchFloor
	}
	return n
}

func lookupWeight(weights map[string]float64, name string) float64 {
	if w, ok := weights[name]; ok {
		return w
	}
	return 1.0
}

func lookupReg(regOrder map[string]int, name string) int {
	if ro, ok := regOrder[name]; ok {
		return ro
	}
	return math.MaxInt
}
