package rank

import (
	"fmt"
	"math"
	"math/rand"
	"testing"

	"github.com/openmtx/meta-search-tool/internal/engine"
)

// These are the spec §14 "property tests for RRF scoring" — randomized,
// deterministic-seeded tests asserting the invariants RRF must hold regardless
// of input. They complement the hand-written golden tests in rank_test.go.
//
// The headline invariant is determinism under input reordering (§14:
// "deterministic ordering under goroutine reordering"): the merged output must
// not depend on which engine goroutine happened to finish first.

// TestProperty_RankDeterministicUnderRunPermutation: for many randomly
// generated engine-run sets, permuting the ORDER of the input runs must not
// change the merged output — not the URL order, not any score, not any result's
// engines[] list. This is the property that makes mst's output reproducible run
// to run despite concurrent dispatch.
func TestProperty_RankDeterministicUnderRunPermutation(t *testing.T) {
	rng := rand.New(rand.NewSource(1)) //nolint:gosec // deterministic test seed
	const trials = 60

	for trial := 0; trial < trials; trial++ {
		numEngines := 2 + rng.Intn(4) // 2..5 engines

		// A pool of distinct canonical URLs the engines will rank in random order.
		poolSize := 3 + rng.Intn(6) // 3..8
		pool := make([]string, poolSize)
		for i := range pool {
			pool[i] = fmt.Sprintf("https://example.com/p%d", i)
		}

		weights := map[string]float64{}
		regOrder := map[string]int{}
		base := make([]engine.EngineRun, 0, numEngines)
		for e := 0; e < numEngines; e++ {
			name := fmt.Sprintf("e%d", e)
			weights[name] = 0.5 + rng.Float64()*2.0 // [0.5, 2.5)
			regOrder[name] = e
			// Each engine ranks a random subset of the pool in a random order
			// (distinct URLs within an engine — the realistic case).
			nres := 1 + rng.Intn(poolSize)
			order := rng.Perm(poolSize)
			res := make([]engine.Result, 0, nres)
			for j := 0; j < nres; j++ {
				res = append(res, engine.Result{URL: pool[order[j]], Title: name})
			}
			base = append(base, engine.EngineRun{Engine: name, Results: res})
		}

		ref := Rank(cloneRuns(base), weights, regOrder, DefaultK)

		// Several independent shuffles of the input run order must all yield the
		// identical merged output.
		for p := 0; p < 8; p++ {
			shuf := cloneRuns(base)
			rng.Shuffle(len(shuf), func(i, j int) { shuf[i], shuf[j] = shuf[j], shuf[i] })
			got := Rank(shuf, weights, regOrder, DefaultK)
			if !sameRanked(ref, got) {
				t.Fatalf("trial %d perm %d: merged output changed under run reordering\n  base=%+v", trial, p, base)
			}
		}
	}
}

// TestProperty_SingleEngineScoreMonotonicity: with a single engine (w>0, k>0),
// the merged score is strictly decreasing in native rank (§9 step 1:
// s = w/(r+k) is strictly decreasing in r). This is the §14 "score monotonicity"
// invariant — a higher native rank must always score higher for one engine.
func TestProperty_SingleEngineScoreMonotonicity(t *testing.T) {
	rng := rand.New(rand.NewSource(2)) //nolint:gosec // deterministic test seed
	const trials = 40

	for trial := 0; trial < trials; trial++ {
		n := 3 + rng.Intn(10)         // 3..12 distinct results
		w := 0.25 + rng.Float64()*3.0 // [0.25, 3.25)
		k := 10 + rng.Intn(90)        // [10, 100)

		res := make([]engine.Result, n)
		for i := range res {
			res[i] = engine.Result{URL: fmt.Sprintf("https://example.com/r%d", i), Title: "t"}
		}
		runs := []engine.EngineRun{{Engine: "solo", Results: res}}
		got := Rank(runs, map[string]float64{"solo": w}, map[string]int{"solo": 0}, k)

		if len(got) != n {
			t.Fatalf("trial %d: got %d results, want %d", trial, len(got), n)
		}
		for i, r := range got {
			// Sorted output position i ↔ native rank i (strictly decreasing).
			want := w / (float64(i) + float64(k))
			if math.Abs(r.Score-want) > 1e-12 {
				t.Errorf("trial %d pos %d: score=%.12f want %.12f (w=%g k=%d)", trial, i, r.Score, want, w, k)
			}
			if i > 0 && got[i-1].Score <= r.Score {
				t.Errorf("trial %d: scores not strictly decreasing at %d: %v", trial, i, scoresOf(got))
			}
		}
	}
}

// TestProperty_DedupSumsContributions: when N engines all return the SAME
// canonical URL at native rank 0 with equal weight, the merged entry's score is
// exactly N·w/k and engines[] lists all N (equal contribution → registration
// order). This pins the §9 dedup/merge rule (score = Σ weighted contributions).
func TestProperty_DedupSumsContributions(t *testing.T) {
	const url = "https://www.example.com/merged?utm_source=x" // has a www + tracking param to also exercise canonicalization
	for n := 1; n <= 6; n++ {
		runs := make([]engine.EngineRun, n)
		w := map[string]float64{}
		ro := map[string]int{}
		for i := 0; i < n; i++ {
			name := fmt.Sprintf("e%d", i)
			runs[i] = engine.EngineRun{Engine: name, Results: []engine.Result{{URL: url, Snippet: name}}}
			w[name] = 1.0
			ro[name] = i
		}
		got := Rank(runs, w, ro, 60)
		if len(got) != 1 {
			t.Fatalf("n=%d: want 1 merged entry, got %d", n, len(got))
		}
		want := float64(n) / 60.0
		if math.Abs(got[0].Score-want) > 1e-12 {
			t.Errorf("n=%d: merged score=%.12f want %.12f", n, got[0].Score, want)
		}
		if len(got[0].Engines) != n {
			t.Errorf("n=%d: engines[] len=%d want %d", n, len(got[0].Engines), n)
		}
	}
}

// TestProperty_AllScoresPositive: every merged result has a strictly positive
// score when all weights are positive and k>0 (each RRF contribution w/(r+k) is
// positive). A non-positive score would indicate a sign/weight bug.
func TestProperty_AllScoresPositive(t *testing.T) {
	rng := rand.New(rand.NewSource(3)) //nolint:gosec // deterministic test seed
	const trials = 40

	for trial := 0; trial < trials; trial++ {
		numEngines := 1 + rng.Intn(3) // 1..3
		poolSize := 2 + rng.Intn(5)
		pool := make([]string, poolSize)
		for i := range pool {
			pool[i] = fmt.Sprintf("https://example.com/x%d", i)
		}
		weights := map[string]float64{}
		regOrder := map[string]int{}
		runs := []engine.EngineRun{}
		for e := 0; e < numEngines; e++ {
			name := fmt.Sprintf("e%d", e)
			weights[name] = 0.1 + rng.Float64()*2.9 // [0.1, 3.0) — always positive
			regOrder[name] = e
			nres := 1 + rng.Intn(poolSize)
			order := rng.Perm(poolSize)
			res := make([]engine.Result, 0, nres)
			for j := 0; j < nres; j++ {
				res = append(res, engine.Result{URL: pool[order[j]]})
			}
			runs = append(runs, engine.EngineRun{Engine: name, Results: res})
		}
		k := 1 + rng.Intn(99) // >0
		got := Rank(runs, weights, regOrder, k)
		for _, r := range got {
			if r.Score <= 0 {
				t.Fatalf("trial %d: non-positive score %v\n  runs=%+v weights=%v k=%d", trial, r.Score, runs, weights, k)
			}
		}
	}
}

// === helpers ================================================================

// cloneRuns returns a deep copy of runs so callers can mutate/reorder without
// aliasing the original slice (the determinism test permutes a copy).
func cloneRuns(runs []engine.EngineRun) []engine.EngineRun {
	out := make([]engine.EngineRun, len(runs))
	for i, r := range runs {
		results := make([]engine.Result, len(r.Results))
		copy(results, r.Results)
		out[i] = engine.EngineRun{Engine: r.Engine, Results: results, Err: r.Err, Latency: r.Latency}
	}
	return out
}

// sameRanked compares two merged outputs by every serialized field (the sort
// keys are excluded — they're derived from the visible fields). Used by the
// determinism property test to assert byte-stable output under reordering.
func sameRanked(a, b []RankedResult) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		x, y := a[i], b[i]
		if x.URL != y.URL || x.Title != y.Title || x.Snippet != y.Snippet {
			return false
		}
		if math.Abs(x.Score-y.Score) > 1e-12 {
			return false
		}
		if !sliceEq(x.Engines, y.Engines) {
			return false
		}
	}
	return true
}

func scoresOf(rs []RankedResult) []float64 {
	out := make([]float64, len(rs))
	for i, r := range rs {
		out[i] = r.Score
	}
	return out
}
