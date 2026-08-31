package web

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lineleader/lineleader/internal/ledger"
)

// budgetEndpoint builds the /trips/{id}/budget URL used by both
// setBudgetOverride and clearBudgetOverride.
func budgetEndpoint(base string, tripID int64) string {
	return base + "/trips/" + strconv.FormatInt(tripID, 10) + "/budget"
}

// postBudgetOverride submits the override form against tripID's budget
// endpoint.
func postBudgetOverride(t *testing.T, base string, tripID int64, value string) *http.Response {
	t.Helper()
	resp, err := http.PostForm(budgetEndpoint(base, tripID), url.Values{"budget": {value}})
	if err != nil {
		t.Fatalf("POST budget override: %v", err)
	}
	return resp
}

// TestSetBudgetOverride_PersistsAndNarrowsResults proves POST
// /trips/{id}/budget stores the override, that it survives a fresh GET, and
// that the search results actually narrow to respect the new, lower budget
// — not merely that the number got stored.
func TestSetBudgetOverride_PersistsAndNarrowsResults(t *testing.T) {
	ts, store := newLedgerTestServer(t)
	defer ts.Close()
	ctx := context.Background()

	// minimalChart: TST, studio, Jan 2026, 10pts Sun-Thu / 14pts Fri-Sat.
	addBudgetContract(t, store, 1000, time.January)

	id := createTripViaForm(t, ts.URL, url.Values{
		"name":       {"Narrow trip"},
		"from":       {"2026-01-05"},
		"to":         {"2026-01-20"},
		"min_nights": {"3"},
	})

	before := body(t, httpDo(t, http.MethodGet, ts.URL+"/trips/"+strconv.FormatInt(id, 10)))
	beforeRows := strings.Count(before, `class="select-btn"`)
	if beforeRows == 0 {
		t.Fatalf("precondition: expected at least one result row before narrowing, got body:\n%s", before)
	}

	resp := postBudgetOverride(t, ts.URL, id, "80")
	got := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("setBudgetOverride status = %d, want 200, body:\n%s", resp.StatusCode, got)
	}

	stored, err := store.GetTrip(ctx, id)
	if err != nil {
		t.Fatalf("GetTrip: %v", err)
	}
	if stored.BudgetOverride == nil || *stored.BudgetOverride != 80 {
		t.Fatalf("stored BudgetOverride = %v, want *80", stored.BudgetOverride)
	}

	// A fresh GET must still show the override, not just the POST response.
	page := body(t, httpDo(t, http.MethodGet, ts.URL+"/trips/"+strconv.FormatInt(id, 10)))
	if !strings.Contains(page, "<dt>Total</dt><dd>80</dd>") {
		t.Errorf("expected the Total row to show the override (80) on a fresh GET, got:\n%s", page)
	}

	// The result set must actually narrow: either fewer rows than before,
	// or every remaining row is within the new budget.
	afterRows := strings.Count(page, `class="select-btn"`)
	afterPoints := extractResultPoints(t, page)
	for _, p := range afterPoints {
		if p > 80 {
			t.Errorf("result row with %d points exceeds the 80-point override, got page:\n%s", p, page)
		}
	}
	if afterRows >= beforeRows {
		t.Errorf("result rows after narrowing = %d, want fewer than %d (before narrowing)", afterRows, beforeRows)
	}
}

// TestSetBudgetOverride_ZeroIsAccepted proves an override of 0 is stored as
// a pointer to 0 (not nil, which would silently fall back to the computed
// budget) and reflected on the page.
func TestSetBudgetOverride_ZeroIsAccepted(t *testing.T) {
	ts, store := newLedgerTestServer(t)
	defer ts.Close()
	ctx := context.Background()

	addBudgetContract(t, store, 200, time.January)

	id := createTripViaForm(t, ts.URL, url.Values{
		"name":       {"Zero override trip"},
		"from":       {"2026-01-05"},
		"to":         {"2026-01-20"},
		"min_nights": {"3"},
	})

	resp := postBudgetOverride(t, ts.URL, id, "0")
	got := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("setBudgetOverride status = %d, want 200, body:\n%s", resp.StatusCode, got)
	}

	stored, err := store.GetTrip(ctx, id)
	if err != nil {
		t.Fatalf("GetTrip: %v", err)
	}
	if stored.BudgetOverride == nil {
		t.Fatalf("stored BudgetOverride = nil, want a pointer to 0")
	}
	if *stored.BudgetOverride != 0 {
		t.Fatalf("stored BudgetOverride = %d, want 0", *stored.BudgetOverride)
	}

	if !strings.Contains(got, "<dt>Total</dt><dd>0</dd>") {
		t.Errorf("expected the Total row to show 0, got:\n%s", got)
	}
}

// TestSetBudgetOverride_NegativeIsRejected proves a negative budget is
// rejected inline (200, not a Postgres constraint error surfaced as a
// 500/400) and that the stored trip is left completely unchanged.
func TestSetBudgetOverride_NegativeIsRejected(t *testing.T) {
	ts, store := newLedgerTestServer(t)
	defer ts.Close()
	ctx := context.Background()

	addBudgetContract(t, store, 200, time.January)

	id := createTripViaForm(t, ts.URL, url.Values{
		"name":       {"Negative override trip"},
		"from":       {"2026-01-05"},
		"to":         {"2026-01-20"},
		"min_nights": {"3"},
	})

	before, err := store.GetTrip(ctx, id)
	if err != nil {
		t.Fatalf("GetTrip (before): %v", err)
	}

	resp := postBudgetOverride(t, ts.URL, id, "-5")
	got := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("setBudgetOverride(-5) status = %d, want 200 (inline error), body:\n%s", resp.StatusCode, got)
	}
	if !strings.Contains(got, `class="err"`) {
		t.Errorf("expected an inline error in the response body, got:\n%s", got)
	}

	after, err := store.GetTrip(ctx, id)
	if err != nil {
		t.Fatalf("GetTrip (after): %v", err)
	}
	if after.BudgetOverride != before.BudgetOverride {
		t.Fatalf("stored BudgetOverride changed: before = %v, after = %v, want unchanged", before.BudgetOverride, after.BudgetOverride)
	}
	if after.BudgetOverride != nil {
		t.Fatalf("stored BudgetOverride = %v, want nil (unchanged)", after.BudgetOverride)
	}
}

// TestSetBudgetOverride_NonNumericIsRejected mirrors the negative case for
// unparseable input.
func TestSetBudgetOverride_NonNumericIsRejected(t *testing.T) {
	ts, store := newLedgerTestServer(t)
	defer ts.Close()
	ctx := context.Background()

	addBudgetContract(t, store, 200, time.January)

	id := createTripViaForm(t, ts.URL, url.Values{
		"name":       {"Non-numeric override trip"},
		"from":       {"2026-01-05"},
		"to":         {"2026-01-20"},
		"min_nights": {"3"},
	})

	before, err := store.GetTrip(ctx, id)
	if err != nil {
		t.Fatalf("GetTrip (before): %v", err)
	}

	resp := postBudgetOverride(t, ts.URL, id, "not-a-number")
	got := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("setBudgetOverride(not-a-number) status = %d, want 200 (inline error), body:\n%s", resp.StatusCode, got)
	}
	if !strings.Contains(got, `class="err"`) {
		t.Errorf("expected an inline error in the response body, got:\n%s", got)
	}

	after, err := store.GetTrip(ctx, id)
	if err != nil {
		t.Fatalf("GetTrip (after): %v", err)
	}
	if after.BudgetOverride != before.BudgetOverride {
		t.Fatalf("stored BudgetOverride changed: before = %v, after = %v, want unchanged", before.BudgetOverride, after.BudgetOverride)
	}
}

// TestClearBudgetOverride_RestoresTheComputedBudget proves DELETE
// /trips/{id}/budget clears the stored override back to nil and that the
// page's effective budget reverts to the computed total.
func TestClearBudgetOverride_RestoresTheComputedBudget(t *testing.T) {
	ts, store := newLedgerTestServer(t)
	defer ts.Close()
	ctx := context.Background()

	addBudgetContract(t, store, 200, time.January)

	id := createTripViaForm(t, ts.URL, url.Values{
		"name":       {"Clear override trip"},
		"from":       {"2026-01-05"},
		"to":         {"2026-01-20"},
		"min_nights": {"3"},
	})

	// Capture the computed total before any override is applied.
	preOverride := body(t, httpDo(t, http.MethodGet, ts.URL+"/trips/"+strconv.FormatInt(id, 10)))
	computedTotal := extractTotal(t, preOverride)

	if resp := postBudgetOverride(t, ts.URL, id, "1"); resp.StatusCode != http.StatusOK {
		t.Fatalf("setBudgetOverride status = %d, want 200, body:\n%s", resp.StatusCode, body(t, resp))
	} else {
		body(t, resp)
	}

	resp := httpDo(t, http.MethodDelete, budgetEndpoint(ts.URL, id))
	got := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("clearBudgetOverride status = %d, want 200, body:\n%s", resp.StatusCode, got)
	}

	stored, err := store.GetTrip(ctx, id)
	if err != nil {
		t.Fatalf("GetTrip: %v", err)
	}
	if stored.BudgetOverride != nil {
		t.Fatalf("stored BudgetOverride = %v, want nil after clearing", *stored.BudgetOverride)
	}

	afterTotal := extractTotal(t, got)
	if afterTotal != computedTotal {
		t.Errorf("Total after clearing override = %d, want %d (the computed total)", afterTotal, computedTotal)
	}
}

// extractTotal pulls the integer out of the budget-total row rendered by
// trip.html's trip_budget sub-template
// (<div class="budget-row budget-total"><dt>Total</dt><dd>N</dd></div>).
func extractTotal(t *testing.T, html string) int {
	t.Helper()
	const marker = `budget-total"><dt>Total</dt><dd>`
	idx := strings.Index(html, marker)
	if idx < 0 {
		t.Fatalf("expected budget-total row in body, got:\n%s", html)
	}
	rest := html[idx+len(marker):]
	end := strings.Index(rest, "<")
	if end < 0 {
		t.Fatalf("malformed budget-total row in body, got:\n%s", html)
	}
	n, err := strconv.Atoi(strings.TrimSpace(rest[:end]))
	if err != nil {
		t.Fatalf("parsing Total value %q: %v", rest[:end], err)
	}
	return n
}

// TestSetBudgetOverride_PreservesFiltersAndOtherFields is the trap that
// already bit updateTrip once (see its doc comment in handlers.go): setting
// the budget override must not disturb the trip's FilterMode/exclusion
// columns.
func TestSetBudgetOverride_PreservesFiltersAndOtherFields(t *testing.T) {
	ts, store := newLedgerTestServerWithCharts(t, twoResortCharts())
	defer ts.Close()
	ctx := context.Background()

	addBudgetContract(t, store, 200, time.January)

	id := createTripViaForm(t, ts.URL, url.Values{
		"name":       {"Filtered override trip"},
		"from":       {"2026-01-05"},
		"to":         {"2026-01-10"},
		"min_nights": {"2"},
	})

	// Put the trip into filter-override mode with an exclusion.
	if resp, err := http.PostForm(ts.URL+"/trips/"+strconv.FormatInt(id, 10)+"/filters/mode", url.Values{"mode": {"override"}}); err != nil {
		t.Fatal(err)
	} else {
		body(t, resp)
	}
	if resp, err := http.Post(ts.URL+"/trips/"+strconv.FormatInt(id, 10)+"/filters/resorts/TS2", "", nil); err != nil {
		t.Fatal(err)
	} else {
		body(t, resp)
	}

	before, err := store.GetTrip(ctx, id)
	if err != nil {
		t.Fatalf("GetTrip (before): %v", err)
	}
	if before.FilterMode != ledger.TripFilterOverride {
		t.Fatalf("precondition: FilterMode = %q, want override", before.FilterMode)
	}
	if len(before.ExcludeResorts) != 1 || before.ExcludeResorts[0] != "TS2" {
		t.Fatalf("precondition: ExcludeResorts = %v, want [TS2]", before.ExcludeResorts)
	}

	resp := postBudgetOverride(t, ts.URL, id, "50")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("setBudgetOverride status = %d, want 200, body:\n%s", resp.StatusCode, body(t, resp))
	} else {
		body(t, resp)
	}

	after, err := store.GetTrip(ctx, id)
	if err != nil {
		t.Fatalf("GetTrip (after): %v", err)
	}
	if after.BudgetOverride == nil || *after.BudgetOverride != 50 {
		t.Fatalf("after BudgetOverride = %v, want *50", after.BudgetOverride)
	}
	if after.FilterMode != ledger.TripFilterOverride {
		t.Errorf("FilterMode after budget override = %q, want unchanged override", after.FilterMode)
	}
	if len(after.ExcludeResorts) != 1 || after.ExcludeResorts[0] != "TS2" {
		t.Errorf("ExcludeResorts after budget override = %v, want unchanged [TS2]", after.ExcludeResorts)
	}
	if after.Name != before.Name || !after.StartDate.Equal(before.StartDate) || !after.EndDate.Equal(before.EndDate) || after.MinNights != before.MinNights {
		t.Errorf("other trip fields changed: before %+v, after %+v", before, after)
	}
}

// TestBudgetOverride_UnknownTripIs404 proves both routes 404 on an unknown
// trip id, via h.getTripOr404 — matching every other per-trip handler's
// existence check.
func TestBudgetOverride_UnknownTripIs404(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	t.Run("set", func(t *testing.T) {
		resp := postBudgetOverride(t, ts.URL, 999999, "50")
		got := body(t, resp)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("setBudgetOverride(unknown id) status = %d, want 404, body:\n%s", resp.StatusCode, got)
		}
	})

	t.Run("clear", func(t *testing.T) {
		resp := httpDo(t, http.MethodDelete, budgetEndpoint(ts.URL, 999999))
		got := body(t, resp)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("clearBudgetOverride(unknown id) status = %d, want 404, body:\n%s", resp.StatusCode, got)
		}
	})
}

// TestTripPage_OffersTheOverrideControls proves the page actually exposes
// the affordances to set and clear the budget override — two earlier
// commits in this feature shipped routes with no UI entry point, so this
// asserts the controls exist and point at the right URLs, not just that the
// routes work when hit directly.
func TestTripPage_OffersTheOverrideControls(t *testing.T) {
	ts, store := newLedgerTestServer(t)
	defer ts.Close()

	addBudgetContract(t, store, 200, time.January)

	id := createTripViaForm(t, ts.URL, url.Values{
		"name":       {"Controls trip"},
		"from":       {"2026-01-05"},
		"to":         {"2026-01-20"},
		"min_nights": {"3"},
	})
	idStr := strconv.FormatInt(id, 10)

	notOverridden := body(t, httpDo(t, http.MethodGet, ts.URL+"/trips/"+idStr))
	if !strings.Contains(notOverridden, `hx-post="/trips/`+idStr+`/budget"`) {
		t.Errorf("expected the not-overridden page to offer a form posting to /trips/%s/budget, got:\n%s", idStr, notOverridden)
	}
	if strings.Contains(notOverridden, `hx-delete="/trips/`+idStr+`/budget"`) {
		t.Errorf("expected no reset-to-computed affordance before an override is set, got:\n%s", notOverridden)
	}

	computedTotal := extractTotal(t, notOverridden)

	if resp := postBudgetOverride(t, ts.URL, id, "50"); resp.StatusCode != http.StatusOK {
		t.Fatalf("setBudgetOverride status = %d, want 200, body:\n%s", resp.StatusCode, body(t, resp))
	} else {
		body(t, resp)
	}

	overridden := body(t, httpDo(t, http.MethodGet, ts.URL+"/trips/"+idStr))
	if !strings.Contains(overridden, `hx-delete="/trips/`+idStr+`/budget"`) {
		t.Errorf("expected the overridden page to offer a reset control deleting /trips/%s/budget, got:\n%s", idStr, overridden)
	}
	// Match the affordance's OWN text precisely ("reset to computed (N)"),
	// not a bare Contains(computedTotal) anywhere on the page: the dimmed
	// computed breakdown also renders numbers derived from the same
	// contract (e.g. CurrentLabel), so a bare substring check would still
	// pass even if the button wired EffectiveBudget in by mistake — this
	// bit a mutation test during development (ixe.15's mutation (f)).
	wantReset := "reset to computed (" + strconv.Itoa(computedTotal) + ")"
	if !strings.Contains(overridden, wantReset) {
		t.Errorf("expected %q in the reset affordance, got:\n%s", wantReset, overridden)
	}
	// The 50-point override must NOT be what the reset button offers.
	if strings.Contains(overridden, "reset to computed (50)") {
		t.Errorf("reset affordance offers the override value, not the computed total, got:\n%s", overridden)
	}
}
