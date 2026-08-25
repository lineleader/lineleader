package web

import (
	"context"
	"sync"
	"time"

	"github.com/lineleader/lineleader/internal/ledger"
)

// costBasisTTL is how long costProvider caches a fetched CostBasis before
// the next Basis() call re-fetches it from the store. The planner's htmx
// endpoints fire on a 300ms input debounce — without caching, every
// keystroke-triggered search would re-query the ledger database for pricing
// data that changes rarely (an edited contract or dues rate), not on every
// render.
const costBasisTTL = 60 * time.Second

// costProvider is the one bridge between the planner and the ledger's cost
// model: it hands the planner a read-only, time-boxed ledger.CostBasis
// snapshot, never a *ledger.Store — internal/dvc never sees the ledger at
// all. It is safe for concurrent use across the web layer's HTTP handlers,
// which may call Basis from multiple goroutines at once.
//
// Basis never returns an error: when there is no store configured
// (Options.Ledger == nil, the case in ~20 existing planner tests, though
// never in production — cmd/server/main.go exits without a DSN) or a fetch
// attempt fails, it degrades to ok=false rather than 500-ing a trip search.
// A ledger blip is a UI degradation — the planner falls back to its
// pre-cost rendering — never an outage.
//
// Ledger handlers keep calling Store.CostBasis directly for their own
// low-traffic, freshness-sensitive rendering (see ledger_view.go); they
// touch costProvider only to Invalidate() it after a successful mutation.
type costProvider struct {
	mu sync.Mutex

	// fetch reads a fresh CostBasis from the store. It is nil when there is
	// no ledger configured, in which case Basis always reports ok=false.
	fetch func(context.Context) (ledger.CostBasis, error)
	// now is a test seam for controlling TTL expiry deterministically;
	// production code always sets it to time.Now.
	now func() time.Time

	basis     ledger.CostBasis
	ok        bool
	fetchedAt time.Time
}

// newCostProvider builds a costProvider backed by store. store may be nil —
// see costProvider's doc comment for the nil-store contract.
func newCostProvider(store *ledger.Store) *costProvider {
	p := &costProvider{now: time.Now}
	if store != nil {
		p.fetch = store.CostBasis
	}
	return p
}

// Basis returns the current CostBasis, refreshing it from fetch when the
// cached one is missing, stale (costBasisTTL or older) or was explicitly
// discarded by Invalidate. ok is false — and basis is CostBasis{}'s zero
// value, i.e. Known() == false — when there is no store, or the refresh
// attempt failed; callers should render their pre-cost UI in that case,
// exactly as if no contract or dues data existed yet.
//
// The fetch (when one runs) happens while p.mu is held, so if ctx is
// cancelled mid-fetch — the caller's HTTP request was cancelled or timed
// out — this degrades to ok=false and releases the lock like any other
// fetch error; the next caller simply retries the fetch with its own
// context. That's correct, just occasionally duplicated work: one client
// disconnecting never poisons the cache for anyone else.
func (p *costProvider) Basis(ctx context.Context) (ledger.CostBasis, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.fetch == nil {
		return ledger.CostBasis{}, false
	}
	if p.ok && p.now().Sub(p.fetchedAt) < costBasisTTL {
		return p.basis, true
	}

	basis, err := p.fetch(ctx)
	if err != nil {
		// Degrade rather than serve a possibly very-stale basis as if it
		// were fresh: a blip means the planner stops showing costs until
		// the next successful fetch, the same honest failure mode as
		// having no ledger configured at all.
		p.ok = false
		return ledger.CostBasis{}, false
	}
	p.basis, p.ok, p.fetchedAt = basis, true, p.now()
	return p.basis, true
}

// Invalidate discards the cached CostBasis so the next Basis() call
// refetches immediately, regardless of costBasisTTL. Ledger mutation
// handlers (addContract, updateContract, deleteContract, upsertDues,
// deleteDues) call this after a successful change so an edited contract or
// dues rate reaches the planner without waiting out the cache.
//
// p may be nil (belt and braces, matching this package's defensive style
// elsewhere — see ledger.CostBasis.compoundDues): every real construction
// path wires a non-nil costProvider, but a nil receiver here degrades to a
// no-op rather than a panic.
func (p *costProvider) Invalidate() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ok = false
}
