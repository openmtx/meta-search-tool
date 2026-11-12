package rank

import (
	"strings"
	"testing"

	"github.com/openmtx/meta-search-tool/internal/engine"
)

func TestNormalizeText(t *testing.T) {
	if got, want := normalizeText("  a\n\t b   c  ", 100), "a b c"; got != want {
		t.Errorf("whitespace = %q, want %q", got, want)
	}
	if got := normalizeText("short", 100); got != "short" {
		t.Errorf("short = %q", got)
	}
	long := strings.Repeat("word ", 100)
	got := normalizeText(long, 20)
	if !strings.HasSuffix(got, " …") {
		t.Errorf("truncated text should end with ellipsis: %q", got)
	}
	if len([]rune(got)) > 22 {
		t.Errorf("truncated text too long: %d runes (%q)", len([]rune(got)), got)
	}
	// A single long token is hard-cut (no space to break at).
	if got := normalizeText(strings.Repeat("x", 30), 10); got != strings.Repeat("x", 10)+" …" {
		t.Errorf("hard cut = %q", got)
	}
	// Unicode is counted in runes, not bytes.
	if got := normalizeText(strings.Repeat("é", 12), 10); got != strings.Repeat("é", 10)+" …" {
		t.Errorf("unicode cut = %q", got)
	}
}

func TestNormalizeSnippet(t *testing.T) {
	if got := NormalizeSnippet("Title", "Title"); got != "" {
		t.Errorf("duplicate snippet = %q, want empty", got)
	}
	if got := NormalizeSnippet("Title", "  a\n b "); got != "a b" {
		t.Errorf("snippet = %q, want %q", got, "a b")
	}
}

func TestRankNormalizesOutput(t *testing.T) {
	runs := []engine.EngineRun{{
		Engine: "e",
		Results: []engine.Result{{
			Title:   strings.Repeat("T", 300),
			URL:     "https://example.com/1",
			Snippet: strings.Repeat("s", 1500),
		}},
	}}
	got := Rank(runs, map[string]float64{"e": 1}, map[string]int{"e": 0}, DefaultK)
	if len(got) != 1 {
		t.Fatalf("got %d results, want 1", len(got))
	}
	if len([]rune(got[0].Title)) > MaxTitleChars+2 {
		t.Errorf("title not capped: %d runes", len([]rune(got[0].Title)))
	}
	if len([]rune(got[0].Snippet)) > MaxSnippetChars+2 {
		t.Errorf("snippet not capped: %d runes", len([]rune(got[0].Snippet)))
	}
}
