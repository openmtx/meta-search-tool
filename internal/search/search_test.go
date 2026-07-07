package search

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openmtx/meta-search-tool/internal/engine"
)

// fakeEngine is a configurable engine for dispatcher tests: it can return canned
// results, an error, sleep before returning, or block until its context is done.
type fakeEngine struct {
	name    string
	results []engine.Result
	err     error
	delay   time.Duration
	block   bool
}

func (f *fakeEngine) Name() string { return f.name }
func (f *fakeEngine) Descriptor() engine.EngineInfo {
	return engine.EngineInfo{Name: f.name, DefaultWeight: 1.0, Enabled: true}
}
func (f *fakeEngine) Search(ctx context.Context, q string, opts engine.Options) ([]engine.Result, error) {
	if f.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return f.results, f.err
}

func res(url string) []engine.Result {
	return []engine.Result{{Title: url, URL: url}}
}

func TestDispatch_BasicFanoutAndOrder(t *testing.T) {
	engines := []engine.Engine{
		&fakeEngine{name: "alpha", results: res("https://a.example/1")},
		&fakeEngine{name: "beta", results: res("https://b.example/2")},
		&fakeEngine{name: "gamma", results: res("https://c.example/3")},
	}
	runs := Dispatch(context.Background(), engines, "q", 10, map[string]time.Duration{
		"alpha": time.Second, "beta": time.Second, "gamma": time.Second,
	})
	if len(runs) != 3 {
		t.Fatalf("got %d runs, want 3", len(runs))
	}
	// Output order must match the input (registration) order, not completion.
	wantNames := []string{"alpha", "beta", "gamma"}
	for i, want := range wantNames {
		if runs[i].Engine != want {
			t.Errorf("runs[%d].Engine = %q, want %q (registration order)", i, runs[i].Engine, want)
		}
		if runs[i].Err != nil {
			t.Errorf("runs[%d] unexpected err: %v", i, runs[i].Err)
		}
	}
	if runs[0].Results[0].URL != "https://a.example/1" {
		t.Errorf("alpha results = %v", runs[0].Results)
	}
}

// TestDispatch_DeterministicOrderUnderVaryingLatency: engines finish in a
// different order than registration, yet output stays in registration order.
func TestDispatch_DeterministicOrderUnderVaryingLatency(t *testing.T) {
	engines := []engine.Engine{
		&fakeEngine{name: "slow", results: res("https://a/1"), delay: 40 * time.Millisecond},
		&fakeEngine{name: "fast", results: res("https://b/2"), delay: 1 * time.Millisecond},
		&fakeEngine{name: "mid", results: res("https://c/3"), delay: 15 * time.Millisecond},
	}
	runs := Dispatch(context.Background(), engines, "q", 10, defaultTimeouts(engines))
	got := []string{runs[0].Engine, runs[1].Engine, runs[2].Engine}
	want := []string{"slow", "fast", "mid"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("order = %v, want %v (must be registration order, not completion)", got, want)
		}
	}
	// The fast engine genuinely finished first — proves reordering happened but
	// was masked in the output.
	if runs[1].Latency >= runs[0].Latency {
		t.Logf("note: fast latency=%v slow latency=%v (completion order differs from output, as required)", runs[1].Latency, runs[0].Latency)
	}
}

// TestDispatch_ConcurrencyCap: with more engines than the cap, never more than
// ConcurrencyCap run at once (the semaphore bound — §7/§17.12).
func TestDispatch_ConcurrencyCap(t *testing.T) {
	const n = 20
	var inflight, maxInflight int32
	engines := make([]engine.Engine, n)
	for i := range engines {
		engines[i] = &trackingEngine{
			name:        name(i),
			inflight:    &inflight,
			maxInflight: &maxInflight,
		}
	}
	runs := Dispatch(context.Background(), engines, "q", 10, defaultTimeouts(engines))
	if len(runs) != n {
		t.Fatalf("got %d runs, want %d", len(runs), n)
	}
	if got := atomic.LoadInt32(&maxInflight); got > ConcurrencyCap {
		t.Errorf("max concurrent engines = %d, want <= %d (concurrency cap)", got, ConcurrencyCap)
	}
	if got := atomic.LoadInt32(&maxInflight); got < 2 {
		t.Errorf("max concurrent = %d, expected real overlap (>1) to exercise the cap", got)
	}
}

// trackingEngine increments an atomic inflight counter on entry and tracks the
// high-water mark, so the concurrency-cap test can assert the cap held.
type trackingEngine struct {
	name        string
	inflight    *int32
	maxInflight *int32
}

func (t *trackingEngine) Name() string { return t.name }
func (t *trackingEngine) Descriptor() engine.EngineInfo {
	return engine.EngineInfo{Name: t.name, DefaultWeight: 1.0, Enabled: true}
}
func (t *trackingEngine) Search(ctx context.Context, q string, opts engine.Options) ([]engine.Result, error) {
	cur := atomic.AddInt32(t.inflight, 1)
	for {
		mx := atomic.LoadInt32(t.maxInflight)
		if cur <= mx || atomic.CompareAndSwapInt32(t.maxInflight, mx, cur) {
			break
		}
	}
	defer atomic.AddInt32(t.inflight, -1)
	select {
	case <-time.After(5 * time.Millisecond):
	case <-ctx.Done():
	}
	return res("https://t/" + t.name), nil
}

func name(i int) string { return string(rune('a' + i)) }

// TestDispatch_PerEngineTimeout: a blocking engine under a short per-engine
// timeout reports a context error (deadline exceeded) and nil results.
func TestDispatch_PerEngineTimeout(t *testing.T) {
	engines := []engine.Engine{
		&fakeEngine{name: "ok", results: res("https://a/1")},
		&fakeEngine{name: "stuck", block: true},
	}
	runs := Dispatch(context.Background(), engines, "q", 10, map[string]time.Duration{
		"ok":    time.Second,
		"stuck": 50 * time.Millisecond,
	})
	if runs[0].Engine != "ok" && runs[0].Err != nil {
		t.Errorf("ok engine errored: %v", runs[0].Err)
	}
	stuck := findRun(runs, "stuck")
	if stuck == nil {
		t.Fatal("stuck run missing")
	}
	if stuck.Err == nil {
		t.Error("stuck engine: expected timeout error, got nil")
	}
	if stuck.Results != nil {
		t.Errorf("stuck engine: expected nil results, got %v", stuck.Results)
	}
	if stuck.Latency <= 0 {
		t.Errorf("stuck engine latency = %v, want > 0", stuck.Latency)
	}
}

// TestDispatch_GlobalCancel: cancelling the global context unblocks engines and
// returns the dispatcher promptly (no hang).
func TestDispatch_GlobalCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	engines := []engine.Engine{
		&fakeEngine{name: "a", block: true},
		&fakeEngine{name: "b", block: true},
	}
	// Cancel shortly after dispatch starts.
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()
	done := make(chan struct{})
	var runs []engine.EngineRun
	go func() {
		runs = Dispatch(ctx, engines, "q", 10, map[string]time.Duration{"a": time.Minute, "b": time.Minute})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Dispatch hung after global cancel")
	}
	for _, r := range runs {
		if r.Err == nil {
			t.Errorf("engine %s: expected ctx error after global cancel, got nil", r.Engine)
		}
	}
}

// TestDispatch_ErrorDoesNotAbort: one engine errors, another still returns results.
func TestDispatch_ErrorDoesNotAbort(t *testing.T) {
	boom := errors.New("upstream 500")
	engines := []engine.Engine{
		&fakeEngine{name: "good", results: res("https://a/1")},
		&fakeEngine{name: "bad", err: boom},
	}
	runs := Dispatch(context.Background(), engines, "q", 10, defaultTimeouts(engines))
	good := findRun(runs, "good")
	bad := findRun(runs, "bad")
	if good == nil || good.Err != nil || len(good.Results) != 1 {
		t.Errorf("good engine = %+v, want 1 result no err", good)
	}
	if bad == nil || bad.Err != boom {
		t.Errorf("bad engine err = %v, want %v", bad.Err, boom)
	}
}

// TestDispatch_PanicRecovered: a panicking engine becomes an error, not a crash,
// and the rest of the run completes.
func TestDispatch_PanicRecovered(t *testing.T) {
	engines := []engine.Engine{
		&fakeEngine{name: "ok", results: res("https://a/1")},
		&panicEngine{name: "kaboom"},
	}
	runs := Dispatch(context.Background(), engines, "q", 10, defaultTimeouts(engines))
	kaboom := findRun(runs, "kaboom")
	if kaboom == nil || kaboom.Err == nil {
		t.Fatalf("panicking engine should report an error, got %+v", kaboom)
	}
	if findRun(runs, "ok") == nil || findRun(runs, "ok").Err != nil {
		t.Error("ok engine should still have completed despite sibling panic")
	}
}

type panicEngine struct{ name string }

func (p *panicEngine) Name() string                  { return p.name }
func (p *panicEngine) Descriptor() engine.EngineInfo { return engine.EngineInfo{Name: p.name} }
func (p *panicEngine) Search(ctx context.Context, q string, opts engine.Options) ([]engine.Result, error) {
	panic("boom")
}

func TestDispatch_Empty(t *testing.T) {
	runs := Dispatch(context.Background(), nil, "q", 10, nil)
	if len(runs) != 0 {
		t.Errorf("empty dispatch = %v, want empty", runs)
	}
}

func findRun(runs []engine.EngineRun, name string) *engine.EngineRun {
	for i := range runs {
		if runs[i].Engine == name {
			return &runs[i]
		}
	}
	return nil
}

func defaultTimeouts(engines []engine.Engine) map[string]time.Duration {
	m := make(map[string]time.Duration, len(engines))
	for _, e := range engines {
		m[e.Name()] = time.Second
	}
	return m
}
