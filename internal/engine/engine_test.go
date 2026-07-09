package engine

import (
	"context"
	"testing"
)

func TestRegistry_RegisterAndGet(t *testing.T) {
	reg := NewRegistry()

	mockEng := &mockEngine{name: "test-engine"}
	if err := reg.Register(mockEng); err != nil {
		t.Fatalf("Register() error: %v", err)
	}

	got := reg.Get("test-engine")
	if got == nil {
		t.Fatal("Get() returned nil for registered engine")
	}
	if got.Name() != "test-engine" {
		t.Errorf("Name() = %q, want %q", got.Name(), "test-engine")
	}

	// Non-existent engine.
	if reg.Get("nonexistent") != nil {
		t.Error("Get() should return nil for unregistered engine")
	}
}

func TestRegistry_DuplicateRegister(t *testing.T) {
	reg := NewRegistry()

	mockEng := &mockEngine{name: "dup"}
	if err := reg.Register(mockEng); err != nil {
		t.Fatalf("first Register() error: %v", err)
	}

	if err := reg.Register(&mockEngine{name: "dup"}); err == nil {
		t.Error("Register() expected error for duplicate name, got nil")
	}
}

func TestRegistry_AllAndNames(t *testing.T) {
	reg := NewRegistry()

	engines := []*mockEngine{
		{name: "alpha"},
		{name: "beta"},
		{name: "gamma"},
	}
	for _, e := range engines {
		if err := reg.Register(e); err != nil {
			t.Fatalf("Register(%s) error: %v", e.name, err)
		}
	}

	all := reg.All()
	if len(all) != len(engines) {
		t.Errorf("All() returned %d engines, want %d", len(all), len(engines))
	}

	names := reg.Names()
	if len(names) != len(engines) {
		t.Errorf("Names() returned %d names, want %d", len(names), len(engines))
	}

	// Verify all names are present.
	nameSet := make(map[string]bool)
	for _, n := range names {
		nameSet[n] = true
	}
	for _, e := range engines {
		if !nameSet[e.name] {
			t.Errorf("Names() missing engine %q", e.name)
		}
	}
}

func TestRegistry_InfosAndOrder(t *testing.T) {
	reg := NewRegistry()

	// Register out of alphabetical order to prove registration order is preserved
	// (not Go map-iteration order) — the ranker depends on this (§9).
	for _, n := range []string{"gamma", "alpha", "beta"} {
		if err := reg.Register(&mockEngine{name: n}); err != nil {
			t.Fatalf("Register(%s) error: %v", n, err)
		}
	}

	want := []string{"gamma", "alpha", "beta"}

	// Names() preserves registration order.
	names := reg.Names()
	if len(names) != len(want) {
		t.Fatalf("Names() = %v, want %v", names, want)
	}
	for i, w := range want {
		if names[i] != w {
			t.Errorf("Names()[%d] = %q, want %q (registration order not preserved)", i, names[i], w)
		}
	}

	// Infos() enumerates via Descriptor() in the same registration order.
	infos := reg.Infos()
	if len(infos) != len(want) {
		t.Fatalf("Infos() returned %d, want %d", len(infos), len(want))
	}
	for i, w := range want {
		if infos[i].Name != w {
			t.Errorf("Infos()[%d].Name = %q, want %q", i, infos[i].Name, w)
		}
	}
}

func TestStatusString(t *testing.T) {
	tests := []struct {
		status Status
		want   string
	}{
		{StatusReady, "ready"},
		{StatusMissingKey, "missing-key"},
		{StatusDisabled, "disabled"},
		{Status(99), "unknown"},
	}

	for _, tt := range tests {
		if got := tt.status.String(); got != tt.want {
			t.Errorf("Status(%d).String() = %q, want %q", tt.status, got, tt.want)
		}
	}
}

// mockEngine is a minimal engine implementation for testing.
type mockEngine struct {
	name     string
	category string
	keyed    bool
}

func (m *mockEngine) Name() string { return m.name }
func (m *mockEngine) Descriptor() EngineInfo {
	return EngineInfo{Name: m.name, Category: m.category, Keyed: m.keyed}
}
func (m *mockEngine) Search(ctx context.Context, q string, opts Options) ([]Result, error) {
	return nil, nil
}

func TestRegistry_ExpandNames(t *testing.T) {
	reg := NewRegistry()
	for _, e := range []*mockEngine{
		{name: "e1", category: "general"},              // keyless
		{name: "e2", category: "general", keyed: true}, // keyed
		{name: "e3", category: "academic"},             // keyless
		{name: "e4", category: "academic"},             // keyless
		{name: "e5", category: "", keyed: true},        // no category, keyed
	} {
		if err := reg.Register(e); err != nil {
			t.Fatalf("Register(%s): %v", e.name, err)
		}
	}

	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{"engine name passes through", []string{"e1"}, []string{"e1"}},
		{"category expands", []string{"academic"}, []string{"e3", "e4"}},
		{"empty category (no match)", []string{""}, []string{""}},
		{"unknown name passes through", []string{"nonexistent"}, []string{"nonexistent"}},
		{"all alias", []string{"all"}, []string{"e1", "e2", "e3", "e4", "e5"}},
		{"free alias (keyless only)", []string{"free"}, []string{"e1", "e3", "e4"}},
		{"keyed alias (keyed only)", []string{"keyed"}, []string{"e2", "e5"}},
		{"mixed category + engine", []string{"general", "e3"}, []string{"e1", "e2", "e3"}},
		{"dedup: engine overlaps its category", []string{"academic", "e3"}, []string{"e3", "e4"}},
		{"dedup: engine in multiple inputs", []string{"e1", "e1"}, []string{"e1"}},
		{"category + free does not dup keyless", []string{"academic", "free"}, []string{"e3", "e4", "e1"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := reg.ExpandNames(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("ExpandNames(%v) = %v (len=%d), want %v (len=%d)",
					tt.in, got, len(got), tt.want, len(tt.want))
			}
			for i, w := range tt.want {
				if got[i] != w {
					t.Errorf("ExpandNames(%v)[%d] = %q, want %q", tt.in, i, got[i], w)
				}
			}
		})
	}
}
