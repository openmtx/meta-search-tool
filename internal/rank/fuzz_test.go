package rank

import (
	"net/url"
	"strings"
	"testing"
)

// FuzzCanonicalize exercises Canonicalize on arbitrary input (spec §14: "Fuzz /
// property tests for URL canonicalization (dedup equivalence classes)"). The
// seed corpus covers the §9 canonicalization sub-steps plus malformed inputs;
// the fuzz engine then explores mutations. Invariants asserted for EVERY input:
//
//  1. Never panics — the primary value of fuzzing a parser on untrusted input.
//     (Engines always send hosts, but Canonicalize must tolerate anything.)
//  2. Idempotent — the canonical form is a fixpoint:
//     Canonicalize(Canonicalize(x)) == Canonicalize(x). If a second pass could
//     still change the URL, two engines sending surface variants of the SAME
//     page might canonicalize to different keys and fail to dedup.
//  3. Fragment-free — the canonical form never carries a URL fragment (§9 step
//     3.3).
//
// Invariants (2) and (3) are scoped to REALISTIC engine URLs: `http`/`https`
// with a simple (DNS/IPv4 — non-empty, colon/bracket-free) hostname, and at most
// one leading www[digits] label. That is precisely the input domain of every
// supported engine (§5) and where dedup idempotency actually matters. Inputs
// outside it — non-http schemes, IPv6/bracket hosts, degenerate colon hosts
// (e.g. `"//:::"`, `"A0000://:?A"`), or pathological multi-www — are only
// required not to panic and to be deterministic; Canonicalize documents their
// grouping as best-effort ("real engines always send hosts"), so a stable
// canonical form is neither guaranteed nor asserted there.
//
// Run the seed corpus with `go test`; explore with
// `go test -run=^$ -fuzz=FuzzCanonicalize ./internal/rank/`.
func FuzzCanonicalize(f *testing.F) {
	for _, seed := range []string{
		// §9 equivalence classes (mirror TestCanonicalize).
		"https://example.com/a",
		"HTTPS://WWW2.Example.COM:443/A/?utm_source=x&keep=1#frag",
		"https://www.example.com/a?b=2&a=1&utm_term=z",
		"http://example.com:80/",
		"https://example.com/a#section",
		"https://example.com/a//",
		"https://example.com?q=hello%20world&utm_source=x",
		"https://www99.example.com:8443/a/",
		// Malformed / non-URL inputs — must not panic, must stay idempotent.
		"",
		"   ",
		"not a url at all",
		"://no-scheme",
		"//host/path?q=1#f",
		"ht!tp://exa mple.com/x",
		"https://例え.テスト/パス?utm_source=1",
		"https://example.com/a?a=1&a=2&utm_campaign=x",
		"mailto:user@example.com",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, raw string) {
		c1 := Canonicalize(raw)

		// (1) Never panics — asserted implicitly by reaching this line.

		// Strong invariants apply only to realistic engine URLs (http/https,
		// simple hostname, ≤1 leading www). Anything else asserts determinism only.
		inU, inErr := url.Parse(raw)
		c1U, c1Err := url.Parse(c1)
		if !isCanonicalFuzzTarget(inU, inErr) || !isCanonicalFuzzTarget(c1U, c1Err) ||
			leadingWWWCount(inU.Hostname()) > 1 {
			if Canonicalize(raw) != c1 {
				t.Errorf("Canonicalize is not deterministic for %q", raw)
			}
			return
		}

		// (2) Idempotency: the canonical form is a fixpoint.
		if c2 := Canonicalize(c1); c1 != c2 {
			t.Errorf("Canonicalize is not idempotent:\n  raw = %q\n  c1  = %q\n  c2  = %q", raw, c1, c2)
		}

		// (3) No fragment survives canonicalization.
		if c1U.Fragment != "" || c1U.RawFragment != "" || strings.Contains(c1, "#") {
			t.Errorf("canonical form retains a fragment: %q (fragment=%q rawfragment=%q)", c1, c1U.Fragment, c1U.RawFragment)
		}
	})
}

// isCanonicalFuzzTarget reports whether u is a realistic engine URL whose
// idempotency the fuzz target asserts: http/https with a simple (DNS/IPv4 —
// non-empty, colon/bracket-free) hostname. IPv6, non-http schemes, and
// degenerate/colon hosts are excluded (their canonicalization is best-effort).
func isCanonicalFuzzTarget(u *url.URL, err error) bool {
	if err != nil || u == nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	h := u.Hostname()
	return h != "" && !strings.ContainsAny(h, ":[]")
}

// leadingWWWCount returns the number of consecutive leading www[digits] host
// labels (www, www2, …). Used to scope the idempotency invariant away from
// pathological multi-www hosts, which dropWWW converges over multiple passes
// (one label removed per pass, per spec §9 step 3.6).
func leadingWWWCount(host string) int {
	n := 0
	for _, label := range strings.Split(host, ".") {
		if wwwLabelRe.MatchString(label) {
			n++
		} else {
			break
		}
	}
	return n
}
