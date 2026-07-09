package format

import (
	"fmt"
	"strings"
	"text/tabwriter"
	"unicode/utf8"

	"github.com/openmtx/meta-search-tool/internal/rank"
)

// MarshalTableUnmerged renders --no-merge results in a fixed-width table format.
// Unlike the merged table, each row is prefixed with the engine name (since
// results are grouped per-engine). Columns: ENGINE, TITLE, URL, SNIPPET.
func MarshalTableUnmerged(results []UnmergedResult) []byte {
	if len(results) == 0 {
		return []byte("(no results)\n")
	}

	var buf strings.Builder
	tw := tabwriter.NewWriter(&buf, 0, 2, 2, ' ', 0)

	// Header row.
	fmt.Fprintln(tw, "ENGINE\tTITLE\tURL\tSNIPPET")

	for _, r := range results {
		title := truncate(r.Title, maxTitleWidth)
		snippet := truncate(r.Snippet, maxSnippetWidth)
		urlStr := truncateURL(r.URL, maxURLWidth)
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r.Engine, title, urlStr, snippet)
	}

	tw.Flush()
	return []byte(buf.String())
}

const (
	// maxTitleWidth is the maximum width reserved for the title column. Titles
	// longer than this are truncated with "…".
	maxTitleWidth = 50

	// maxSnippetWidth is the maximum width reserved for the snippet column.
	// Snippets longer than this are truncated with "…".
	maxSnippetWidth = 80

	// maxURLWidth is the maximum width reserved for the URL column. URLs longer
	// than this are truncated, keeping the last segment (path) visible.
	maxURLWidth = 50

	// minColumnWidth is the minimum width for any column (ensures readability).
	minColumnWidth = 4

	// truncationSuffix is appended to truncated text.
	truncationSuffix = "…"
)

// MarshalTable renders results in a fixed-width table format suitable for
// terminal display. Columns: RANK, TITLE, URL, SNIPPET. The table uses
// tabwriter for alignment; columns are separated by two spaces. Long titles
// and snippets are truncated to fit within maxTitleWidth / maxSnippetWidth.

// MarshalTable renders merged results in a fixed-width table format suitable
// for terminal display. Columns: RANK, TITLE, URL, SNIPPET. Long titles and
// snippets are truncated with "…" to fit within column width limits. The
// table is right-aligned on the rank column and left-aligned on everything
// else.

func MarshalTable(results []rank.RankedResult) []byte {
	if len(results) == 0 {
		return []byte("(no results)\n")
	}

	var buf strings.Builder
	tw := tabwriter.NewWriter(&buf, 0, 2, 2, ' ', 0)

	// Header row.
	fmt.Fprintln(tw, "RANK\tTITLE\tURL\tSNIPPET")

	for _, r := range results {
		title := truncate(r.Title, maxTitleWidth)
		snippet := truncate(r.Snippet, maxSnippetWidth)
		urlStr := truncateURL(r.URL, maxURLWidth)
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\n", r.Rank, title, urlStr, snippet)
	}

	tw.Flush()
	return []byte(buf.String())
}

// truncate shortens s to at most maxRune runes, appending truncationSuffix if
// truncated. It operates on rune boundaries (not bytes) so multi-byte Unicode
// characters are never split. If s fits within maxRune runes it is returned
// unchanged.
func truncate(s string, maxRune int) string {
	if utf8.RuneCountInString(s) <= maxRune {
		return s
	}
	// Take maxRune-1 runes for content + 1 for the suffix.
	runes := []rune(s)[:maxRune-utf8.RuneCountInString(truncationSuffix)]
	return string(runes) + truncationSuffix
}

// truncateURL shortens a URL to at most maxRune runes. It keeps the scheme and
// host intact (the identifying part) and truncates the path. This produces
// more readable output than simple prefix truncation.
func truncateURL(urlStr string, maxRune int) string {
	if utf8.RuneCountInString(urlStr) <= maxRune {
		return urlStr
	}

	// Try to keep scheme://host and a bit of path.
	schemeEnd := strings.Index(urlStr, "://")
	if schemeEnd >= 0 {
		hostStart := schemeEnd + 3
		hostEnd := strings.Index(urlStr[hostStart:], "/")
		if hostEnd > 0 {
			host := urlStr[:hostStart+hostEnd]
			remaining := maxRune - utf8.RuneCountInString(host) - utf8.RuneCountInString(truncationSuffix)
			if remaining > 3 {
				path := urlStr[hostStart+hostEnd:]
				return host + truncate(path, remaining)
			}
			// No room for path — just show scheme://host…
			return host + truncationSuffix
		}
	}

	// Fallback: simple rune truncation.
	return truncate(urlStr, maxRune)
}
