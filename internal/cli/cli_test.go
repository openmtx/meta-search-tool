package cli

import (
	"errors"
	"os"
	"testing"
)

func TestParse_BasicFlags(t *testing.T) {
	origArgs := os.Args
	defer func() { os.Args = origArgs }()

	os.Args = []string{"mst", "--limit", "5", "--format", "jsonl"}

	flags, err := Parse()
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
	origArgs := os.Args
	defer func() { os.Args = origArgs }()

	os.Args = []string{"mst", "--version"}

	flags, err := Parse()
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}

	if !flags.Version {
		t.Error("Version should be true")
	}
}

func TestParse_ListEnginesFlag(t *testing.T) {
	origArgs := os.Args
	defer func() { os.Args = origArgs }()

	os.Args = []string{"mst", "--list-engines"}

	flags, err := Parse()
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}

	if !flags.ListEngines {
		t.Error("ListEngines should be true")
	}
}

func TestParse_WeightFlag(t *testing.T) {
	origArgs := os.Args
	defer func() { os.Args = origArgs }()

	os.Args = []string{"mst", "--weight", "brave=2.0", "--weight", "ddg=0.5"}

	flags, err := Parse()
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
	origArgs := os.Args
	defer func() { os.Args = origArgs }()

	os.Args = []string{"mst", "--stdin", "some query"}

	_, err := Parse()
	if err == nil {
		t.Error("Parse() expected error for --stdin + positional args, got nil")
	}
}

func TestParse_WeightBadFloat(t *testing.T) {
	origArgs := os.Args
	defer func() { os.Args = origArgs }()

	os.Args = []string{"mst", "--weight", "brave=notafloat", "q"}

	_, err := Parse()
	if err == nil {
		t.Fatal("Parse() expected error for non-numeric weight, got nil")
	}
}

func TestParse_PerEngineTimeout(t *testing.T) {
	origArgs := os.Args
	defer func() { os.Args = origArgs }()

	os.Args = []string{"mst", "--per-engine-timeout", "3s", "q"}

	flags, err := Parse()
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	if flags.PerEngineTimeout != "3s" {
		t.Errorf("PerEngineTimeout = %q, want \"3s\"", flags.PerEngineTimeout)
	}
}

func TestParse_ShortFlags(t *testing.T) {
	origArgs := os.Args
	defer func() { os.Args = origArgs }()

	// -e/-n/-f are aliases of --engine/--limit/--format (spec §4).
	os.Args = []string{"mst", "-e", "brave,ddg", "-n", "5", "-f", "jsonl", "query"}

	flags, err := Parse()
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
	origArgs := os.Args
	defer func() { os.Args = origArgs }()

	// -vv is the verbosity count flag at level 2 (spec §4).
	os.Args = []string{"mst", "-vv", "q"}

	flags, err := Parse()
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	if flags.Verbose != 2 {
		t.Errorf("verbose = %d, want 2", flags.Verbose)
	}
}

func TestParse_HelpFlag(t *testing.T) {
	origArgs := os.Args
	defer func() { os.Args = origArgs }()

	os.Args = []string{"mst", "--help"}

	_, err := Parse()
	if !errors.Is(err, ErrHelp) {
		t.Errorf("Parse() err = %v, want ErrHelp", err)
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
