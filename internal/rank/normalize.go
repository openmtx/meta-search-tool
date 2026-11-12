package rank

import "strings"

// Output text limits (§9/§10). They keep JSON/JSONL context bounded without
// changing ranking: upstream SearXNG uses the same 200/1200-character caps.
const (
	// MaxTitleChars is the maximum length of a result title in output.
	MaxTitleChars = 200
	// MaxSnippetChars is the maximum length of a result snippet in output.
	MaxSnippetChars = 1200
)

// NormalizeTitle collapses whitespace and caps title at MaxTitleChars.
func NormalizeTitle(s string) string {
	return normalizeText(s, MaxTitleChars)
}

// NormalizeSnippet collapses whitespace, caps at MaxSnippetChars, and drops a
// snippet that duplicates the normalized title.
func NormalizeSnippet(title, snippet string) string {
	snippet = normalizeText(snippet, MaxSnippetChars)
	if snippet == title {
		return ""
	}
	return snippet
}

// normalizeText collapses all whitespace runs to one space, trims, and truncates
// at a word boundary (runes, not bytes) with a trailing " …" when over max.
func normalizeText(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if s == "" || max <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	cut := runes[:max]
	// Prefer the last full word; fall back to a hard cut when there is no space.
	if i := lastSpace(cut); i > 0 {
		cut = cut[:i]
	}
	return strings.TrimRight(string(cut), " ") + " …"
}

// lastSpace returns the index of the last space rune in s, or -1.
func lastSpace(s []rune) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == ' ' {
			return i
		}
	}
	return -1
}
