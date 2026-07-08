package cli

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestParse_BasicFlags(t *testing.T) {
	flags, err := Parse([]string{"--limit", "5", "--format", "jsonl"}, io.Discard)
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	if flags.Limit != 5 {
		t.Errorf("limit = %d, want 5", flags.Limit)
	}
	if flags.Format != "jsonl" {
		t.Errorf("format = %q, want \"jsonl\"", flags.Format)
	}
}

func TestParse_VersionFlag(t *testing.T) {
	flags, err := Parse([]string{"--version"}, io.Discard)
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	if !flags.Version {
		t.Error("Version should be true")
	}
}

func TestParse_ListEnginesFlag(t *testing.T) {
	flags, err := Parse([]string{"--list-engines"}, io.Discard)
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	if !flags.ListEngines {
		t.Error("ListEngines should be true")
	}
}

func TestParse_WeightFlag(t *testing.T) {
	flags, err := Parse([]string{"--weight", "brave=2.0", "--weight", "ddg=0.5"}, io.Discard)
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	if len(flags.Weight) != 2 {
		t.Fatalf("Weight map has %d entries, want 2", len(flags.Weight))
	}
	if flags.Weight["brave"] != 2.0 {
		t.Errorf("brave weight = %f, want 2.0", flags.Weight["brave"])
	}
	if flags.Weight["ddg"] != 0.5 {
		t.Errorf("ddg weight = %f, want 0.5", flags.Weight["ddg"])
	}
}

func TestParse_StdinAndPositionalConflict(t *testing.T) {
	if _, err := Parse([]string{"--stdin", "some query"}, io.Discard); err == nil {
		t.Error("Parse() expected error for --stdin + positional args, got nil")
	}
}

func TestParse_WeightBadFloat(t *testing.T) {
	if _, err := Parse([]string{"--weight", "brave=notafloat", "q"}, io.Discard); err == nil {
		t.Fatal("Parse() expected error for non-numeric weight, got nil")
	}
}

func TestParse_PerEngineTimeout(t *testing.T) {
	flags, err := Parse([]string{"--per-engine-timeout", "3s", "q"}, io.Discard)
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	if flags.PerEngineTimeout != "3s" {
		t.Errorf("PerEngineTimeout = %q, want \"3s\"", flags.PerEngineTimeout)
	}
}

func TestParse_ShortFlags(t *testing.T) {
	// -e/-n/-f are aliases of --engine/--limit/--format (spec §4).
	flags, err := Parse([]string{"-e", "brave,ddg", "-n", "5", "-f", "jsonl", "query"}, io.Discard)
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	if flags.Limit != 5 {
		t.Errorf("limit = %d, want 5", flags.Limit)
	}
	if flags.Format != "jsonl" {
		t.Errorf("format = %q, want jsonl", flags.Format)
	}
	if len(flags.Engines) != 2 || flags.Engines[0] != "brave" || flags.Engines[1] != "ddg" {
		t.Errorf("engines = %v, want [brave ddg]", flags.Engines)
	}
	if flags.Query != "query" {
		t.Errorf("query = %q, want query", flags.Query)
	}
}

func TestParse_VerboseCount(t *testing.T) {
	// -vv is the verbosity count flag at level 2 (spec §4).
	flags, err := Parse([]string{"-vv", "q"}, io.Discard)
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	if flags.Verbose != 2 {
		t.Errorf("verbose = %d, want 2", flags.Verbose)
	}
}

func TestParse_HelpFlag(t *testing.T) {
	var buf bytes.Buffer
	_, err := Parse([]string{"--help"}, &buf)
	if !errors.Is(err, ErrHelp) {
		t.Errorf("Parse() err = %v, want ErrHelp", err)
	}
	if buf.Len() == 0 {
		t.Error("--help should print usage to the provided writer")
	}
}

// TestParse_Help_GroupedByTier verifies §4's requirement that --help groups
// flags by skill tier (tier-1 flags before tier-2, tier-2 before tier-3), with
// each tier under its header.
func TestParse_Help_GroupedByTier(t *testing.T) {
	var buf bytes.Buffer
	if _, err := Parse([]string{"--help"}, &buf); err == nil {
		t.Fatal("Parse(--help) should return ErrHelp")
	}
	s := buf.String()

	for _, header := range []string{
		"Tier 1 — skill-facing",
		"Tier 2 — tuning",
		"Tier 3 — debug/internal",
	} {
		if !strings.Contains(s, header) {
			t.Errorf("--help missing tier header %q:\n%s", header, s)
		}
	}

	// Ordering: each tier's flags must appear under their own header — a tier-1
	// flag precedes the tier-2 header, a tier-3 flag follows it.
	iEngine := strings.Index(s, "--engine")
	iTier2 := strings.Index(s, "Tier 2")
	iRaw := strings.Index(s, "--raw")
	iTier3 := strings.Index(s, "Tier 3")
	if iEngine < 0 || iTier2 < 0 || iRaw < 0 || iTier3 < 0 {
		t.Fatalf("expected flags/headers not all found:\n%s", s)
	}
	if !(iEngine < iTier2) {
		t.Errorf("tier-1 flag --engine (%d) must precede the Tier 2 header (%d)", iEngine, iTier2)
	}
	if !(iTier3 < iRaw) {
		t.Errorf("tier-3 flag --raw (%d) must follow the Tier 3 header (%d)", iRaw, iTier3)
	}
	// Every registered flag should still be documented somewhere.
	for _, flag := range []string{"--engine", "--exclude", "--limit", "--format", "--weight",
		"--rrf-k", "--require", "--strict", "--timeout",
		"--raw", "--no-merge", "--per-engine-timeout", "--config", "-v", "--verbose"} {
		if !strings.Contains(s, flag) {
			t.Errorf("--help missing flag %q", flag)
		}
	}
}

func TestSplitComma(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"brave,ddg", []string{"brave", "ddg"}},
		{" brave , ddg ", []string{"brave", "ddg"}},
		{"", nil},
		{"brave", []string{"brave"}},
	}
	for _, tt := range tests {
		got := splitComma(tt.input)
		if len(got) != len(tt.want) {
			t.Errorf("splitComma(%q) returned %d items, want %d", tt.input, len(got), len(tt.want))
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("splitComma(%q)[%d] = %q, want %q", tt.input, i, got[i], tt.want[i])
			}
		}
	}
}
