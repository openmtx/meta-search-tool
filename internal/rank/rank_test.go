package rank

import (
	"math"
	"testing"

	"github.com/openmtx/meta-search-tool/internal/engine"
)

// === Canonicalization (§9 step 3) ===========================================

func TestCanonicalize(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		// 1. lowercase scheme + host.
		{"lowercase scheme/host", "HTTPS://Example.COM/Path", "https://example.com/Path"},

		// 2. drop default ports.
		{"drop https default port", "https://example.com:443/a", "https://example.com/a"},
		{"drop http default port", "http://example.com:80/a", "http://example.com/a"},
		{"keep non-default port", "https://example.com:8443/a", "https://example.com:8443/a"},

		// 3. remove fragment.
		{"strip fragment", "https://example.com/a#section", "https://example.com/a"},
		{"strip empty fragment", "https://example.com/a#", "https://example.com/a"},

		// 4. strip trailing slash, preserve bare root.
		{"strip trailing slash", "https://example.com/a/", "https://example.com/a"},
		{"preserve bare root slash", "https://example.com/", "https://example.com/"},
		{"bare root no path -> root", "https://example.com", "https://example.com/"},
		{"collapse multi trailing slash", "https://example.com/a//", "https://example.com/a"},

		// 5. strip tracking params (preserve order + encoding of the rest).
		{"strip utm", "https://example.com/a?utm_source=x&keep=1", "https://example.com/a?keep=1"},
		{"strip all tracking", "https://example.com/a?gclid=1&fbclid=2&msclkid=3&mc_cid=4&mc_eid=5", "https://example.com/a"},
		{"keep ref param", "https://example.com/a?ref=blog", "https://example.com/a?ref=blog"},
		{"preserve order", "https://example.com/a?b=2&a=1&utm_term=z", "https://example.com/a?b=2&a=1"},
		{"preserve encoding", "https://example.com/a?q=hello%20world&utm_source=x", "https://example.com/a?q=hello%20world"},
		{"lowercase param name match", "https://example.com/a?UTM_SOURCE=x&keep=1", "https://example.com/a?keep=1"},

		// 6. drop leading www[ digits] label.
		{"drop www", "https://www.example.com/a", "https://example.com/a"},
		{"drop www2", "https://www2.example.com/a", "https://example.com/a"},
		{"drop www99", "https://www99.example.com/a", "https://example.com/a"},
		{"keep www-like real label", "https://wwwcom.example.com/a", "https://wwwcom.example.com/a"},
		{"keep single-label host", "https://localhost/a", "https://localhost/a"},

		// Combined (the realistic dedup case).
		{
			"combined everything",
			"HTTPS://WWW2.Example.COM:443/A/?utm_source=x&keep=1#frag",
			"https://example.com/A?keep=1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Canonicalize(tt.in); got != tt.want {
				t.Errorf("Canonicalize(%q)\n  got  %q\n  want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestCanonicalize_DedupEquivalence: several surface forms of the same page must
// canonicalize identically (the core dedup invariant).
func TestCanonicalize_DedupEquivalence(t *testing.T) {
	want := "https://example.com/a?keep=1"
	forms := []string{
		"https://example.com/a?keep=1",
		"https://www.example.com/a/?keep=1",
		"HTTPS://Example.COM:443/a/?keep=1&utm_source=x#top",
		"https://www3.example.com/a?keep=1",
		"http://example.com/a?keep=1", // NOTE: different scheme → NOT equivalent (http vs https)
	}
	// The first three are https and equivalent; the last (http) is intentionally
	// distinct (scheme is part of identity). Verify the equivalence class + the
	// http outlier.
	for i := 0; i < 3; i++ {
		if got := Canonicalize(forms[i]); got != want {
			t.Errorf("forms[%d] %q => %q, want %q", i, forms[i], got, want)
		}
	}
	if got := Canonicalize(forms[4]); got == want {
		t.Errorf("http form must NOT canonicalize to the https form; got %q", got)
	}
}

// === Source extraction (§10 source, §17.13) =================================

func TestSourceFromURL(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"https://en.wikipedia.org/wiki/Go", "wikipedia.org"},
		{"https://www.wikipedia.org/wiki/Go", "wikipedia.org"},
		{"https://news.bbc.co.uk/story", "bbc.co.uk"},
		{"https://example.com/a/b/c", "example.com"},
		{"https://sub.deep.example.co.uk/x", "example.co.uk"},
		{"https://example.com", "example.com"},
		{"not a url", ""},
	}
	for _, tt := range tests {
		if got := SourceFromURL(tt.url); got != tt.want {
			t.Errorf("SourceFromURL(%q) = %q, want %q", tt.url, got, tt.want)
		}
	}
}

// === RRF scoring (§9 steps 1–2) =============================================

// TestRank_SpecExample replicates the §10 worked example exactly: brave ranks the
// URL first (weight 1.2), ddg ranks it second (weight 0.8), k=60.
//
//	score = 1.2/(0+60) + 0.8/(1+60) = 0.033114…
//	engines = [brave, ddg]  (brave's 0.0200 contribution > ddg's 0.01311)
func TestRank_SpecExample(t *testing.T) {
	const url = "https://example.com/tokio-vs-async-std"
	runs := []engine.EngineRun{
		{Engine: "brave", Results: []engine.Result{{Title: "brave-title", URL: url, Snippet: "brave-snippet"}}},
		{Engine: "ddg", Results: []engine.Result{
			{Title: "other", URL: "https://example.com/other"},     // rank 0 for ddg
			{Title: "ddg-title", URL: url, Snippet: "ddg-snippet"}, // rank 1 for ddg
		}},
	}
	weights := map[string]float64{"brave": 1.2, "ddg": 0.8}
	regOrder := map[string]int{"brave": 0, "ddg": 1}

	got := Rank(runs, weights, regOrder, 60)
	if len(got) != 2 {
		t.Fatalf("got %d results, want 2", len(got))
	}
	top := got[0]
	if top.URL != url {
		t.Errorf("top URL = %q, want %q", top.URL, url)
	}
	wantScore := 1.2/60.0 + 0.8/61.0
	if !floatEq(top.Score, wantScore) {
		t.Errorf("top score = %.10f, want %.10f", top.Score, wantScore)
	}
	if !floatEq(top.Score, 0.0331147540, 1e-6) { // spec rounds to 0.0331
		t.Errorf("top score = %.6f, spec example says ~0.0331", top.Score)
	}
	// brave contributed more (0.02 > 0.0131), so it leads engines[] AND carries
	// the title/snippet.
	wantEngines := []string{"brave", "ddg"}
	if !sliceEq(top.Engines, wantEngines) {
		t.Errorf("top engines = %v, want %v", top.Engines, wantEngines)
	}
	if top.Title != "brave-title" || top.Snippet != "brave-snippet" {
		t.Errorf("top carried title/snippet = %q/%q, want brave's", top.Title, top.Snippet)
	}
}

// TestRank_Dedup merges surface-form variants of the same canonical URL across
// engines into a single entry whose score is the sum and engines[] is both.
func TestRank_Dedup(t *testing.T) {
	runs := []engine.EngineRun{
		{Engine: "alpha", Results: []engine.Result{
			{Title: "alpha-title", URL: "https://www.example.com/a/", Snippet: "from-alpha"},
		}},
		{Engine: "beta", Results: []engine.Result{
			{Title: "beta-title", URL: "https://example.com/a?utm_source=x", Snippet: "from-beta"},
		}},
	}
	weights := map[string]float64{"alpha": 1.0, "beta": 1.0}
	regOrder := map[string]int{"alpha": 0, "beta": 1}

	got := Rank(runs, weights, regOrder, 60)
	if len(got) != 1 {
		t.Fatalf("dedup: got %d results, want 1 (both forms canonicalize together)", len(got))
	}
	want := 1.0/60.0 + 1.0/60.0
	if !floatEq(got[0].Score, want) {
		t.Errorf("merged score = %.10f, want %.10f", got[0].Score, want)
	}
	// Equal contributions → registration order breaks the tie: alpha first.
	if !sliceEq(got[0].Engines, []string{"alpha", "beta"}) {
		t.Errorf("engines = %v, want [alpha beta]", got[0].Engines)
	}
}

// TestRank_EnginesOrderingByContribution: a heavier-weighted engine leads
// engines[] even when it ranked the URL lower natively.
func TestRank_EnginesOrderingByContribution(t *testing.T) {
	const url = "https://example.com/x"
	runs := []engine.EngineRun{
		{Engine: "heavy", Results: []engine.Result{
			{URL: "https://example.com/other"}, // rank 0
			{Title: "heavy-title", URL: url},   // rank 1
		}},
		{Engine: "light", Results: []engine.Result{{Title: "light-title", URL: url}}}, // rank 0
	}
	// heavy w=2.0 at rank1: 2/61 = 0.032786 ; light w=0.5 at rank0: 0.5/60 = 0.008333.
	// heavy contributes more → leads engines[] and carries the snippet.
	weights := map[string]float64{"heavy": 2.0, "light": 0.5}
	regOrder := map[string]int{"heavy": 0, "light": 1}

	got := Rank(runs, weights, regOrder, 60)
	if len(got) != 2 {
		t.Fatalf("got %d, want 2", len(got))
	}
	top := got[0]
	if top.URL != url {
		t.Fatalf("top URL = %q, want the merged one", top.URL)
	}
	if !sliceEq(top.Engines, []string{"heavy", "light"}) {
		t.Errorf("engines = %v, want [heavy light] (desc contribution)", top.Engines)
	}
	if top.Title != "heavy-title" {
		t.Errorf("title = %q, want heavy-title (highest weighted contribution)", top.Title)
	}
}

// TestRank_SnippetTieBreakRegistration: on an exact weighted-score tie for the
// snippet carrier, the first-registered engine wins (not the goroutine order).
func TestRank_SnippetTieBreakRegistration(t *testing.T) {
	const url = "https://example.com/x"
	// Register beta first (regOrder 0), alpha second. Both contribute equally at
	// rank 0 with equal weight → tie → beta (first registered) carries.
	runs := []engine.EngineRun{
		{Engine: "alpha", Results: []engine.Result{{Title: "alpha-title", URL: url}}},
		{Engine: "beta", Results: []engine.Result{{Title: "beta-title", URL: url}}},
	}
	regOrder := map[string]int{"beta": 0, "alpha": 1}
	got := Rank(runs, map[string]float64{"alpha": 1, "beta": 1}, regOrder, 60)
	if len(got) != 1 {
		t.Fatalf("got %d, want 1", len(got))
	}
	if got[0].Title != "beta-title" {
		t.Errorf("snippet tie title = %q, want beta-title (first registered)", got[0].Title)
	}
	if !sliceEq(got[0].Engines, []string{"beta", "alpha"}) {
		t.Errorf("engines = %v, want [beta alpha] (reg order on tie)", got[0].Engines)
	}
}

// TestRank_DeterministicUnderReordering: shuffling the INPUT run order must not
// change the merged OUTPUT order (the property RRF must guarantee — §14).
func TestRank_DeterministicUnderReordering(t *testing.T) {
	mk := func(order ...string) []engine.EngineRun {
		urls := map[string]string{
			"a": "https://example.com/a",
			"b": "https://example.com/b",
			"c": "https://example.com/c",
		}
		var rs []engine.EngineRun
		for _, n := range order {
			rs = append(rs, engine.EngineRun{Engine: n, Results: []engine.Result{{URL: urls[n], Title: n}}})
		}
		return rs
	}
	weights := map[string]float64{"a": 1, "b": 1, "c": 1}
	regOrder := map[string]int{"a": 0, "b": 1, "c": 2}

	r1 := rankURLs(mk("a", "b", "c"), weights, regOrder)
	r2 := rankURLs(mk("c", "a", "b"), weights, regOrder)
	r3 := rankURLs(mk("b", "c", "a"), weights, regOrder)
	if !sliceEq(r1, r2) || !sliceEq(r1, r3) {
		t.Errorf("output not deterministic under input reordering:\n r1=%v\n r2=%v\n r3=%v", r1, r2, r3)
	}
}

// === Final sort tie-breakers (§9 step 4) ====================================

// TestSortRanked_TieBreakers exercises each tie-break level directly via the
// extracted comparator (canonical-URL fallback is otherwise unreachable through
// Rank because a single engine's per-rank contributions are strictly decreasing).
func TestSortRanked_TieBreakers(t *testing.T) {
	t.Run("b numEngines beats lower score? no — score first", func(t *testing.T) {
		// score is the primary key; a 2-engine entry with slightly lower score
		// still ranks BELOW a 1-engine entry with higher score.
		out := []RankedResult{
			{URL: "lo", Score: 0.5, numEng: 5, topReg: 9, canonical: "lo"},
			{URL: "hi", Score: 0.9, numEng: 1, topReg: 9, canonical: "hi"},
		}
		sortRanked(out)
		if out[0].URL != "hi" {
			t.Errorf("score is primary: got %v first, want hi", out[0].URL)
		}
	})
	t.Run("b numEngines when score ties", func(t *testing.T) {
		out := []RankedResult{
			{URL: "one", Score: 0.5, numEng: 1, topReg: 0, canonical: "z"},
			{URL: "two", Score: 0.5, numEng: 2, topReg: 0, canonical: "a"},
		}
		sortRanked(out)
		if out[0].URL != "two" {
			t.Errorf("on score tie, more engines first: got %v, want two", out[0].URL)
		}
	})
	t.Run("c registration order when score+numEng tie", func(t *testing.T) {
		out := []RankedResult{
			{URL: "late", Score: 0.5, numEng: 1, topReg: 5, canonical: "a"},
			{URL: "early", Score: 0.5, numEng: 1, topReg: 1, canonical: "z"},
		}
		sortRanked(out)
		if out[0].URL != "early" {
			t.Errorf("on score+numEng tie, lower reg first: got %v, want early", out[0].URL)
		}
	})
	t.Run("d canonical URL as total-order fallback", func(t *testing.T) {
		out := []RankedResult{
			{URL: "zeta", Score: 0.5, numEng: 1, topReg: 1, canonical: "https://z.com"},
			{URL: "alpha", Score: 0.5, numEng: 1, topReg: 1, canonical: "https://a.com"},
		}
		sortRanked(out)
		if out[0].URL != "alpha" {
			t.Errorf("canonical fallback: got %v first, want alpha", out[0].URL)
		}
	})
}

// === Windowing (§9 step 5) ==================================================

func TestWindow(t *testing.T) {
	// 5 results, scores 5..1 in ranked order.
	mk := func() []RankedResult {
		out := make([]RankedResult, 5)
		for i := range out {
			out[i] = RankedResult{URL: string(rune('a' + i)), Score: float64(5 - i)}
		}
		return out
	}
	t.Run("limit only", func(t *testing.T) {
		got := Window(mk(), 0, 3)
		if len(got) != 3 || got[0].Rank != 1 || got[2].Rank != 3 {
			t.Errorf("Window(0,3) = %+v ranks", got)
		}
	})
	t.Run("offset + limit", func(t *testing.T) {
		got := Window(mk(), 2, 2)
		if len(got) != 2 || got[0].URL != "c" || got[0].Rank != 1 || got[1].Rank != 2 {
			t.Errorf("Window(2,2) = %+v, want [c,d] ranks 1,2", got)
		}
	})
	t.Run("limit beyond end clamps", func(t *testing.T) {
		got := Window(mk(), 3, 10)
		if len(got) != 2 || got[1].Rank != 2 {
			t.Errorf("Window(3,10) = %+v, want 2 items", got)
		}
	})
	t.Run("offset beyond end empty", func(t *testing.T) {
		got := Window(mk(), 50, 10)
		if len(got) != 0 {
			t.Errorf("Window(50,10) = %+v, want empty", got)
		}
	})
	t.Run("zero limit = all", func(t *testing.T) {
		got := Window(mk(), 0, 0)
		if len(got) != 5 || got[4].Rank != 5 {
			t.Errorf("Window(0,0) = %+v, want all 5 ranked", got)
		}
	})
}

// === Per-engine fetch size (§9) =============================================

func TestFetchLimit(t *testing.T) {
	tests := []struct {
		offset, limit, want int
	}{
		{0, 10, 30},  // (0+10)*3
		{0, 5, 20},   // (0+5)*3 = 15 → floored to 20
		{10, 10, 60}, // (10+10)*3 (deep page grows fetch — §4)
		{0, 0, 20},   // floor
		{1, 1, 20},   // (1+1)*3=6 → floor 20
	}
	for _, tt := range tests {
		if got := FetchLimit(tt.offset, tt.limit); got != tt.want {
			t.Errorf("FetchLimit(%d,%d) = %d, want %d", tt.offset, tt.limit, got, tt.want)
		}
	}
}

// === helpers ================================================================

func rankURLs(runs []engine.EngineRun, weights map[string]float64, regOrder map[string]int) []string {
	out := Rank(runs, weights, regOrder, DefaultK)
	urls := make([]string, len(out))
	for i, r := range out {
		urls[i] = r.URL
	}
	return urls
}

func sliceEq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// floatEq compares two floats within a tolerance (default 1e-12; pass a custom
// tolerance as the optional last arg). RRF scores are floats, so exact == is
// too strict for the spec's rounded examples.
func floatEq(a, b float64, tol ...float64) bool {
	t := 1e-12
	if len(tol) > 0 {
		t = tol[0]
	}
	return math.Abs(a-b) <= t
}
