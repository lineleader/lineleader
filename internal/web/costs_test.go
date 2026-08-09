package web

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/lineleader/lineleader/internal/ledger"
)

// TestCostProvider_NilStoreIsSafe pins the nil-store contract: a planner
// running with no ledger configured (Options.Ledger == nil, the common case
// in ~20 existing planner tests) must never panic and must render its
// pre-cost UI, never a 500.
func TestCostProvider_NilStoreIsSafe(t *testing.T) {
	p := newCostProvider(nil)
	basis, ok := p.Basis()
	if ok {
		t.Errorf("ok = true, want false for a nil store")
	}
	if basis.Known() {
		t.Errorf("basis.Known() = true, want false (zero value) for a nil store")
	}
}

// TestCostProvider_CachesWithinTTL confirms Basis() serves the cached
// CostBasis — without calling fetch again — for calls inside costBasisTTL of
// the first fetch. The planner's htmx endpoints fire on a 300ms input
// debounce; without this, every keystroke would hit the database.
func TestCostProvider_CachesWithinTTL(t *testing.T) {
	calls := 0
	fetch := func() (ledger.CostBasis, error) {
		calls++
		return ledger.CostBasis{}, nil
	}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	p := &costProvider{fetch: fetch, now: func() time.Time { return now }}

	if _, ok := p.Basis(); !ok {
		t.Fatalf("first Basis() ok = false, want true")
	}
	now = now.Add(costBasisTTL - time.Second)
	if _, ok := p.Basis(); !ok {
		t.Fatalf("second Basis() ok = false, want true")
	}
	if calls != 1 {
		t.Errorf("fetch called %d times inside the TTL window, want 1", calls)
	}
}

// TestCostProvider_RefetchesAfterTTL confirms a Basis() call made
// costBasisTTL or later after the last fetch triggers a fresh one.
func TestCostProvider_RefetchesAfterTTL(t *testing.T) {
	calls := 0
	fetch := func() (ledger.CostBasis, error) {
		calls++
		return ledger.CostBasis{}, nil
	}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	p := &costProvider{fetch: fetch, now: func() time.Time { return now }}

	p.Basis()
	now = now.Add(costBasisTTL + time.Second)
	p.Basis()
	if calls != 2 {
		t.Errorf("fetch called %d times, want 2 (once before, once after TTL expiry)", calls)
	}
}

// TestCostProvider_Invalidate confirms Invalidate forces the next Basis()
// call to refetch immediately, regardless of costBasisTTL — this is what
// lets an edited contract or dues rate reach the planner without waiting out
// the cache (see ledger_handlers.go's Invalidate() calls).
func TestCostProvider_Invalidate(t *testing.T) {
	calls := 0
	fetch := func() (ledger.CostBasis, error) {
		calls++
		return ledger.CostBasis{}, nil
	}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	p := &costProvider{fetch: fetch, now: func() time.Time { return now }}

	p.Basis()
	p.Basis()
	if calls != 1 {
		t.Fatalf("fetch called %d times before Invalidate, want 1", calls)
	}
	p.Invalidate()
	p.Basis()
	if calls != 2 {
		t.Errorf("fetch called %d times after Invalidate, want 2", calls)
	}
}

// TestCostProvider_FetchErrorDegrades confirms Basis never returns an error:
// a failed fetch degrades to ok=false (the planner's pre-cost rendering)
// rather than panicking or surfacing the error to the caller.
func TestCostProvider_FetchErrorDegrades(t *testing.T) {
	fetch := func() (ledger.CostBasis, error) {
		return ledger.CostBasis{}, errors.New("boom")
	}
	p := &costProvider{fetch: fetch, now: time.Now}

	basis, ok := p.Basis()
	if ok {
		t.Errorf("ok = true, want false on a failed fetch")
	}
	if basis.Known() {
		t.Errorf("basis.Known() = true, want false on a failed fetch")
	}
}

// TestCostProvider_ConcurrentAccessIsSafe exercises Basis from many
// goroutines at once — costProvider is shared across concurrent HTTP
// handlers, so this must pass under `go test -race`.
func TestCostProvider_ConcurrentAccessIsSafe(t *testing.T) {
	fetch := func() (ledger.CostBasis, error) { return ledger.CostBasis{}, nil }
	p := &costProvider{fetch: fetch, now: time.Now}

	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.Basis()
		}()
	}
	wg.Wait()
}
