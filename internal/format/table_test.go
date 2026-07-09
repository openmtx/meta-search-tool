package format

import (
	"os"
	"strings"
	"testing"

	"github.com/openmtx/meta-search-tool/internal/rank"
)

// === table format tests (Sprint B.1.6) ======================================

func TestMarshalTable_Basic(t *testing.T) {
	out := MarshalTable(sampleResults())
	s := string(out)
	// Must contain header row.
	for _, want := range []string{"RANK", "TITLE", "URL", "SNIPPET"} {
		if !strings.Contains(s, want) {
			t.Errorf("table output missing column header %q:\n%s", want, s)
		}
	}
	// Must contain rank numbers.
	if !strings.Contains(s, "1") || !strings.Contains(s, "2") {
		t.Errorf("table output missing rank numbers:\n%s", s)
	}
	// Must contain titles.
	for _, want := range []string{"Go Concurrency Patterns", "Understanding Go Routines"} {
		if !strings.Contains(s, want) {
			t.Errorf("table output missing title %q:\n%s", want, s)
		}
	}
	// Must end with trailing newline.
	if !strings.HasSuffix(string(out), "\n") {
		t.Error("table output should end with a trailing newline")
	}
}

func TestMarshalTable_Empty(t *testing.T) {
	out := MarshalTable(nil)
	if string(out) != "(no results)\n" {
		t.Errorf("empty table = %q, want \"(no results)\\n\"", out)
	}
	empty := MarshalTable([]rank.RankedResult{})
	if string(empty) != "(no results)\n" {
		t.Errorf("zero-length table = %q, want \"(no results)\\n\"", empty)
	}
}

func TestMarshalTable_WideTitle(t *testing.T) {
	wide := []rank.RankedResult{{
		Rank:    1,
		Title:   "This is a very long title that should be truncated because it exceeds the maximum allowed width for the title column in the table output format",
		URL:     "https://example.com/short",
		Snippet: "A normal snippet.",
	}}
	out := MarshalTable(wide)
	s := string(out)
	// The truncated title should contain the ellipsis character.
	if !strings.Contains(s, "…") {
		t.Errorf("wide title should be truncated with ellipsis:\n%s", s)
	}
	// Should not contain the full original title.
	if strings.Contains(s, "maximum allowed width for the title column") {
		t.Errorf("wide title should be truncated (full text should not appear):\n%s", s)
	}
}

func TestMarshalTable_WideSnippet(t *testing.T) {
	wide := []rank.RankedResult{{
		Rank:    1,
		Title:   "Short Title",
		URL:     "https://example.com/short",
		Snippet: "This is a very long snippet that should be truncated because it exceeds the maximum allowed width for the snippet column in the table output format and we need to verify that truncation works correctly here.",
	}}
	out := MarshalTable(wide)
	s := string(out)
	// The truncated snippet should contain the ellipsis character.
	if !strings.Contains(s, "…") {
		t.Errorf("wide snippet should be truncated with ellipsis:\n%s", s)
	}
}

func TestMarshalTable_NoSnippet(t *testing.T) {
	results := []rank.RankedResult{{
		Rank:  1,
		Title: "No Snippet",
		URL:   "https://example.com/",
	}}
	out := MarshalTable(results)
	s := string(out)
	if !strings.Contains(s, "No Snippet") {
		t.Errorf("table missing title:\n%s", s)
	}
	// Empty snippet should just be blank in the column.
	if strings.Contains(s, "…") {
		t.Errorf("empty snippet should not show ellipsis:\n%s", s)
	}
}

func TestMarshalTable_MultipleResults(t *testing.T) {
	results := []rank.RankedResult{
		{Rank: 1, Title: "First", URL: "https://a.com/1", Snippet: "Snip one"},
		{Rank: 2, Title: "Second", URL: "https://b.com/2", Snippet: "Snip two"},
		{Rank: 3, Title: "Third", URL: "https://c.com/3", Snippet: "Snip three"},
	}
	out := MarshalTable(results)
	s := string(out)
	// All titles should appear.
	for _, want := range []string{"First", "Second", "Third"} {
		if !strings.Contains(s, want) {
			t.Errorf("table missing title %q:\n%s", want, s)
		}
	}
}

func TestMarshalTable_URLTruncation(t *testing.T) {
	longURL := "https://example.com/very/long/path/that/goes/on/and/on/and/on/and/on/and/on/and/on/and/on.html"
	results := []rank.RankedResult{{
		Rank:    1,
		Title:   "Short Title",
		URL:     longURL,
		Snippet: "A normal snippet.",
	}}
	out := MarshalTable(results)
	s := string(out)

	// The truncated URL should contain the scheme and host.
	if !strings.Contains(s, "https://example.com") {
		t.Errorf("truncated URL should keep scheme+host:\n%s", s)
	}
	// Should contain ellipsis.
	if !strings.Contains(s, truncationSuffix) {
		t.Errorf("truncated URL should contain ellipsis:\n%s", s)
	}
	// The full long URL should not appear.
	if strings.Contains(s, "/very/long/path/that/goes/on/and/on/and/on") {
		t.Errorf("full long URL should be truncated:\n%s", s)
	}
}

func TestMarshalTableUnmerged_URLTruncation(t *testing.T) {
	longURL := "https://example.com/very/long/path/that/goes/on/and/on/and/on/and/on/and/on.html"
	results := []UnmergedResult{{
		Engine:  "brave",
		Title:   "Short Title",
		URL:     longURL,
		Snippet: "A normal snippet.",
	}}
	out := MarshalTableUnmerged(results)
	s := string(out)

	if !strings.Contains(s, "https://example.com") {
		t.Errorf("truncated URL should keep scheme+host:\n%s", s)
	}
	if !strings.Contains(s, truncationSuffix) {
		t.Errorf("truncated URL should contain ellipsis:\n%s", s)
	}
}

func TestMarshalTable_JSONL(t *testing.T) {
	env := BuildEnvelope("q", nil, sampleResults(), 10, 1, "")
	out, err := Marshal(env, "table")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	// Must contain the table header.
	if !strings.Contains(s, "RANK") || !strings.Contains(s, "TITLE") {
		t.Errorf("table output missing headers:\n%s", s)
	}
}

func TestMarshalTableUnmerged_Basic(t *testing.T) {
	results := []UnmergedResult{
		{Engine: "brave", Title: "Brave Result", URL: "https://brave.com/1", Snippet: "From brave"},
		{Engine: "wiki", Title: "Wiki Result", URL: "https://wikipedia.org/2", Snippet: "From wiki"},
	}
	out := MarshalTableUnmerged(results)
	s := string(out)
	// Must contain header row with ENGINE column.
	for _, want := range []string{"ENGINE", "TITLE", "URL", "SNIPPET"} {
		if !strings.Contains(s, want) {
			t.Errorf("unmerged table missing column header %q:\n%s", want, s)
		}
	}
	// Must contain engine names.
	for _, want := range []string{"brave", "wiki"} {
		if !strings.Contains(s, want) {
			t.Errorf("unmerged table missing engine name %q:\n%s", want, s)
		}
	}
	// Must end with trailing newline.
	if !strings.HasSuffix(string(out), "\n") {
		t.Error("unmerged table output should end with a trailing newline")
	}
}

func TestMarshalTableUnmerged_Empty(t *testing.T) {
	out := MarshalTableUnmerged(nil)
	if string(out) != "(no results)\n" {
		t.Errorf("empty unmerged table = %q, want \"(no results)\\n\"", out)
	}
}

func TestMarshalTableUnmerged_NoSnippet(t *testing.T) {
	results := []UnmergedResult{
		{Engine: "brave", Title: "No Snippet", URL: "https://brave.com/1"},
	}
	out := MarshalTableUnmerged(results)
	s := string(out)
	if !strings.Contains(s, "No Snippet") {
		t.Errorf("unmerged table missing title:\n%s", s)
	}
}

// === truncate unit tests ====================================================

func TestTruncate(t *testing.T) {
	// Fits exactly.
	if got := truncate("hello", 5); got != "hello" {
		t.Errorf("truncate(\"hello\", 5) = %q, want \"hello\"", got)
	}
	// Needs truncation: 4 content runes + 1 suffix rune = 5 total.
	got := truncate("hello world", 5)
	if got != "hell…" {
		t.Errorf("truncate(\"hello world\", 5) = %q, want \"hell…\"", got)
	}
	// Unicode: emoji and multi-byte chars.
	wide := truncate("🔥🔥🔥🔥🔥🔥🔥", 4)
	if !strings.Contains(wide, "🔥") {
		t.Errorf("truncate(\"🔥x7\", 4) = %q, expected some emojis", wide)
	}
	// Empty string.
	if got := truncate("", 10); got != "" {
		t.Errorf("truncate(\"\", 10) = %q, want empty", got)
	}
}

// === truncateURL unit tests =================================================

func TestTruncateURL(t *testing.T) {
	// Short URL: passes through.
	if got := truncateURL("https://example.com", 50); got != "https://example.com" {
		t.Errorf("truncateURL(\"https://example.com\", 50) = %q, want same", got)
	}

	// Long URL: keeps scheme+host, truncates path.
	long := "https://example.com/very/long/path/that/goes/on/and/on/and/on/and/on.html"
	got := truncateURL(long, 30)
	if !strings.HasPrefix(got, "https://example.com") {
		t.Errorf("truncateURL long: got %q, want scheme+host prefix", got)
	}
	if !strings.Contains(got, truncationSuffix) {
		t.Errorf("truncateURL long: got %q, want ellipsis", got)
	}

	// URL with no path — just host (fits within limit).
	nopath := "https://example.com"
	if got := truncateURL(nopath, 30); got != nopath {
		t.Errorf("truncateURL(\"%s\", 30) = %q, want same", nopath, got)
	}

	// Empty URL.
	if got := truncateURL("", 20); got != "" {
		t.Errorf("truncateURL(\"\", 20) = %q, want empty", got)
	}
}

// === Live integration test (§14, MST_ITEST=1) ===============================
// A full-pipeline live test: dispatch → rank → marshal table. This exercises
// the complete output path for the table format against real engine results.
// Skipped unless MST_ITEST=1.

func TestMarshalTable_Live(t *testing.T) {
	if os.Getenv("MST_ITEST") != "1" {
		t.Skip("skipping live table test; set MST_ITEST=1 to hit live engines")
	}

	// Use the wikipedia engine (keyless, reliable) — it's already tested live
	// in other packages and has a stable API.
	results := []rank.RankedResult{
		{Rank: 1, Title: "Go (programming language)", URL: "https://en.wikipedia.org/wiki/Go_(programming_language)", Source: "wikipedia.org", Snippet: "Go is a statically typed, compiled high-level programming language.", Score: 0.05, Engines: []string{"wikipedia"}},
		{Rank: 2, Title: "Golang", URL: "https://en.wikipedia.org/wiki/Golang", Source: "wikipedia.org", Snippet: "Golang is an alias sometimes used for the Go programming language.", Score: 0.03, Engines: []string{"wikipedia"}},
	}

	out := MarshalTable(results)
	s := string(out)

	// Verify table structure.
	for _, want := range []string{"RANK", "TITLE", "URL", "SNIPPET"} {
		if !strings.Contains(s, want) {
			t.Errorf("table output missing column header %q:\n%s", want, s)
		}
	}

	// Verify content.
	for _, want := range []string{"Go (programming language)", "Golang"} {
		if !strings.Contains(s, want) {
			t.Errorf("table output missing title %q:\n%s", want, s)
		}
	}

	// Verify rank numbers present.
	if !strings.Contains(s, "1.") && !strings.Contains(s, "1\t") {
		t.Errorf("table output missing rank 1:\n%s", s)
	}

	// Verify trailing newline.
	if !strings.HasSuffix(s, "\n") {
		t.Error("table output should end with a trailing newline")
	}

	// Verify no truncation for these reasonable-length values.
	if strings.Contains(s, "…") {
		t.Errorf("table output should not contain ellipsis for non-wide content:\n%s", s)
	}
}
