package main

import (
	"testing"
	"time"

	"github.com/openmtx/meta-search-tool/internal/cli"
	"github.com/openmtx/meta-search-tool/internal/config"
	"github.com/openmtx/meta-search-tool/internal/engine"
)

// regOf builds a registry of simple fakes with given (name, weight, enabled,
// keyed, keyEnv) tuples.
func regOf(t *testing.T, specs ...fakeEngine) *engine.Registry {
	t.Helper()
	r := engine.NewRegistry()
	for _, s := range specs {
		s := s
		if err := r.Register(&s); err != nil {
			t.Fatalf("register %s: %v", s.name, err)
		}
	}
	return r
}

func fe(name string, weight float64, enabled, keyed bool, keyEnv string) fakeEngine {
	return fakeEngine{name: name, weight: weight, enabled: enabled, keyed: keyed, keyEnv: keyEnv}
}

func flagsOf(args ...string) *cli.Flags {
	f, err := cli.Parse(args, nil)
	if err != nil {
		panic(err)
	}
	return f
}

func defaultCfg() *config.Config {
	return &config.Config{Version: 1, Engines: make(map[string]*config.EngineConfig)}
}

func enabledPtr(b bool) *bool { return &b }

// === Engine selection (§4) ==================================================

func TestResolve_Whitelist(t *testing.T) {
	reg := regOf(t, fe("a", 1, true, false, ""), fe("b", 1, true, false, ""), fe("c", 1, true, false, ""))
	p, err := resolvePlan(flagsOf("--engine", "a,c", "q"), defaultCfg(), reg)
	if err != nil {
		t.Fatal(err)
	}
	if names := engineNames(p.engines); !eq(names, []string{"a", "c"}) {
		t.Errorf("whitelist engines = %v, want [a c]", names)
	}
}

func TestResolve_Exclude(t *testing.T) {
	reg := regOf(t, fe("a", 1, true, false, ""), fe("b", 1, true, false, ""), fe("c", 1, true, false, ""))
	p, err := resolvePlan(flagsOf("--exclude", "b", "q"), defaultCfg(), reg)
	if err != nil {
		t.Fatal(err)
	}
	if names := engineNames(p.engines); !eq(names, []string{"a", "c"}) {
		t.Errorf("exclude engines = %v, want [a c]", names)
	}
}

func TestResolve_WhitelistAndExclude(t *testing.T) {
	reg := regOf(t, fe("a", 1, true, false, ""), fe("b", 1, true, false, ""))
	// --engine selects a,b; --exclude removes b → only a.
	p, err := resolvePlan(flagsOf("--engine", "a,b", "--exclude", "b", "q"), defaultCfg(), reg)
	if err != nil {
		t.Fatal(err)
	}
	if names := engineNames(p.engines); !eq(names, []string{"a"}) {
		t.Errorf("engines = %v, want [a] (--exclude wins)", names)
	}
}

func TestResolve_MissingKeyAutoSkipped(t *testing.T) {
	reg := regOf(t,
		fe("brave", 2.0, true, true, "BRAVE_API_KEY"), // keyed, no key set
		fe("wiki", 1.0, true, false, ""),              // keyless
	)
	p, err := resolvePlan(flagsOf("q"), defaultCfg(), reg)
	if err != nil {
		t.Fatal(err)
	}
	if names := engineNames(p.engines); !eq(names, []string{"wiki"}) {
		t.Errorf("engines = %v, want [wiki] (brave auto-skipped, missing key)", names)
	}
}

func TestResolve_MissingKeyPresentRuns(t *testing.T) {
	t.Setenv("BRAVE_API_KEY", "x")
	reg := regOf(t,
		fe("brave", 2.0, true, true, "BRAVE_API_KEY"),
		fe("wiki", 1.0, true, false, ""),
	)
	p, err := resolvePlan(flagsOf("q"), defaultCfg(), reg)
	if err != nil {
		t.Fatal(err)
	}
	if names := engineNames(p.engines); !eq(names, []string{"brave", "wiki"}) {
		t.Errorf("engines = %v, want [brave wiki] (key present)", names)
	}
}

func TestResolve_DisabledByConfig(t *testing.T) {
	reg := regOf(t, fe("a", 1, true, false, ""), fe("b", 1, true, false, ""))
	cfg := &config.Config{Version: 1, Engines: map[string]*config.EngineConfig{
		"b": {Enabled: enabledPtr(false)},
	}}
	p, err := resolvePlan(flagsOf("q"), cfg, reg)
	if err != nil {
		t.Fatal(err)
	}
	if names := engineNames(p.engines); !eq(names, []string{"a"}) {
		t.Errorf("engines = %v, want [a] (b disabled by config)", names)
	}
}

// === Weights & timeouts (§6) ================================================

func TestResolve_WeightPrecedence(t *testing.T) {
	// default 2.0 → config 3.0 → flag 5.0 for "a"; "b" keeps default.
	reg := regOf(t, fe("a", 2.0, true, false, ""), fe("b", 1.5, true, false, ""))
	cfg := &config.Config{Version: 1, Engines: map[string]*config.EngineConfig{
		"a": {Weight: 3.0},
	}}
	p, err := resolvePlan(flagsOf("--weight", "a=5.0", "q"), cfg, reg)
	if err != nil {
		t.Fatal(err)
	}
	if p.weights["a"] != 5.0 {
		t.Errorf("a weight = %v, want 5.0 (flag wins)", p.weights["a"])
	}
	if p.weights["b"] != 1.5 {
		t.Errorf("b weight = %v, want 1.5 (default)", p.weights["b"])
	}
}

func TestResolve_PerEngineTimeoutPrecedence(t *testing.T) {
	reg := regOf(t, fe("a", 1, true, false, ""), fe("b", 1, true, false, ""))
	cfg := &config.Config{Version: 1, Engines: map[string]*config.EngineConfig{
		"a": {Timeout: "2s"}, // config beats the flag/derived default
	}}
	// --timeout 10s → default per-engine = min(10s, 6s) = 6s; but a has config 2s.
	p, err := resolvePlan(flagsOf("--timeout", "10s", "q"), cfg, reg)
	if err != nil {
		t.Fatal(err)
	}
	if p.timeouts["a"] != 2*time.Second {
		t.Errorf("a timeout = %v, want 2s (config)", p.timeouts["a"])
	}
	if p.timeouts["b"] != 6*time.Second {
		t.Errorf("b timeout = %v, want 6s (min(global 10s, 6s ceiling))", p.timeouts["b"])
	}
	if p.globalTo != 10*time.Second {
		t.Errorf("global timeout = %v, want 10s", p.globalTo)
	}
}

func TestResolve_FetchLimitAndRegOrder(t *testing.T) {
	reg := regOf(t, fe("a", 1, true, false, ""), fe("b", 1, true, false, ""))
	p, err := resolvePlan(flagsOf("-n", "10", "q"), defaultCfg(), reg)
	if err != nil {
		t.Fatal(err)
	}
	// max((0+10)*3, 20) = 30
	if p.fetchLimit != 30 {
		t.Errorf("fetchLimit = %d, want 30", p.fetchLimit)
	}
	if p.regOrder["a"] != 0 || p.regOrder["b"] != 1 {
		t.Errorf("regOrder = %v, want a=0 b=1", p.regOrder)
	}
}

// === Validation (§4) ========================================================

func TestResolve_UnknownEngineInEngine(t *testing.T) {
	reg := regOf(t, fe("a", 1, true, false, ""))
	_, err := resolvePlan(flagsOf("--engine", "bogus", "q"), defaultCfg(), reg)
	if err == nil {
		t.Fatal("expected error for unknown engine in --engine")
	}
}

func TestResolve_UnknownEngineInWeight(t *testing.T) {
	reg := regOf(t, fe("a", 1, true, false, ""))
	_, err := resolvePlan(flagsOf("--weight", "bogus=1", "q"), defaultCfg(), reg)
	if err == nil {
		t.Fatal("expected error for unknown engine in --weight")
	}
}

func TestResolve_RequireCannotStart(t *testing.T) {
	reg := regOf(t, fe("brave", 2, true, true, "BRAVE_API_KEY")) // no key set
	_, err := resolvePlan(flagsOf("--require", "brave", "q"), defaultCfg(), reg)
	if err == nil {
		t.Fatal("expected error: --require'd engine missing key cannot start")
	}
}

func TestResolve_RequireExcludeConflict(t *testing.T) {
	reg := regOf(t, fe("a", 1, true, false, ""))
	_, err := resolvePlan(flagsOf("--require", "a", "--exclude", "a", "q"), defaultCfg(), reg)
	if err == nil {
		t.Fatal("expected error for --require ∩ --exclude")
	}
}

func TestResolve_BadGlobalTimeout(t *testing.T) {
	reg := regOf(t, fe("a", 1, true, false, ""))
	_, err := resolvePlan(flagsOf("--timeout", "5x", "q"), defaultCfg(), reg)
	if err == nil {
		t.Fatal("expected error for invalid --timeout")
	}
}

// === helpers ================================================================

func engineNames(engines []engine.Engine) []string {
	out := make([]string, len(engines))
	for i, e := range engines {
		out[i] = e.Name()
	}
	return out
}

func eq(a, b []string) bool {
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
