// Package search implements the dispatcher (§7): it fans a single query out to
// many engines concurrently, each in its own goroutine under its own per-engine
// timeout (nested under a global run context), then collects their output into a
// deterministic, registration-ordered []EngineRun for the ranker.
//
// The dispatcher is the only place that touches goroutines for engine calls. A
// failing or timed-out engine becomes an EngineRun with a non-nil Err — it never
// aborts the run (§7/§11). Output order depends only on registration order, never
// on which goroutine happened to finish first (§9 determinism).
package search

import (
	"context"
	"fmt"
	"runtime/debug"
	"sync"
	"time"

	"github.com/openmtx/meta-search-tool/internal/engine"
)

// ConcurrencyCap is the fixed internal cap on simultaneous outbound engine calls
// (§7 / resolved decision §17.12). It is not configurable — a no-op while fewer
// engines than this are enabled.
const ConcurrencyCap = 8

// Dispatch runs every engine concurrently and returns one EngineRun per engine,
// in the SAME order as the input engines slice (the caller passes them in
// registration order, so the result is deterministic regardless of goroutine
// scheduling — §9).
//
// The global context ctx caps the whole run; each engine additionally gets a
// per-engine timeout (timeouts[name]) implemented as a child
// context.WithTimeout, whose deadline is the earlier of its own budget and the
// global deadline (§4/§7 — "min(per_engine_timeout, remaining_global_timeout)").
// An engine with no entry in timeouts (or a non-positive one) runs under the
// global context only.
//
// A failing/timed-out engine yields {Err: <error>, Results: nil}; it does not
// abort the run. A panicking engine is recovered into an error so it cannot take
// down the others.
func Dispatch(ctx context.Context, engines []engine.Engine, q string, fetchLimit int, timeouts map[string]time.Duration) []engine.EngineRun {
	runs := make([]engine.EngineRun, len(engines))
	if len(engines) == 0 {
		return runs
	}

	var wg sync.WaitGroup
	sem := make(chan struct{}, ConcurrencyCap)

	for i, e := range engines {
		i, e := i, e
		wg.Add(1)
		go func() {
			defer wg.Done()
			runs[i] = runOne(ctx, sem, e, q, fetchLimit, timeouts[e.Name()])
		}()
	}
	wg.Wait()
	return runs
}

// runOne executes a single engine call under the semaphore + per-engine timeout,
// recovering panics, and returns its EngineRun.
func runOne(ctx context.Context, sem chan struct{}, e engine.Engine, q string, fetchLimit int, timeout time.Duration) (run engine.EngineRun) {
	run.Engine = e.Name()

	defer func() {
		if r := recover(); r != nil {
			// A panicking engine must not abort the run (§7); record it as an
			// error and move on. Keep the stack out of the user-facing message
			// (it may be verbose) but retain it for -v diagnostics via the log.
			run.Results = nil
			run.Err = fmt.Errorf("engine %s panicked: %v", e.Name(), r)
			_ = debug.Stack() // available for future -v logging
		}
	}()

	// Acquire a concurrency slot, but don't block forever if the global deadline
	// already fired while we were queued.
	select {
	case sem <- struct{}{}:
		defer func() { <-sem }()
	case <-ctx.Done():
		run.Err = ctx.Err()
		return
	}

	runCtx := ctx
	if timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	start := time.Now()
	results, err := e.Search(runCtx, q, engine.Options{Limit: fetchLimit})
	run.Results, run.Err, run.Latency = results, err, time.Since(start)
	return
}
