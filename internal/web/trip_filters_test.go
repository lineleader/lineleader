package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lineleader/lineleader/internal/ledger"
)

// newTripFiltersTestServer is newLedgerTestServerWithCharts, wired with
// twoResortCharts (TST / TS2) and a budget contract, so a test can create
// trips and immediately see search results without repeating the setup.
func newTripFiltersTestServer(t *testing.T) (*httptest.Server, *ledger.Store) {
	t.Helper()
	ts, store := newLedgerTestServerWithCharts(t, twoResortCharts())
	addBudgetContract(t, store, 200, time.January)
	return ts, store
}

// TestTripFiltersPanel_RendersTripScopedURLs proves the per-trip panel's
// hx-post/hx-delete URLs carry the trip's actual id, not a positional index.
// Two trips are created and the SECOND one's panel is checked, with the
// assertion on the literal id rather than a plausible index.
func TestTripFiltersPanel_RendersTripScopedURLs(t *testing.T) {
	ts, store := newTripFiltersTestServer(t)
	defer ts.Close()
	ctx := context.Background()

	_, err := store.AddTrip(ctx, ledger.Trip{
		Name: "First trip", StartDate: dateParse(t, "2026-01-05"), EndDate: dateParse(t, "2026-01-10"), MinNights: 2,
	})
	if err != nil {
		t.Fatalf("AddTrip 1: %v", err)
	}
	id, err := store.AddTrip(ctx, ledger.Trip{
		Name: "Second trip", StartDate: dateParse(t, "2026-01-05"), EndDate: dateParse(t, "2026-01-10"), MinNights: 2,
		FilterMode: ledger.TripFilterOverride,
	})
	if err != nil {
		t.Fatalf("AddTrip 2: %v", err)
	}
	idStr := strconv.FormatInt(id, 10)

	resp, err := http.Get(ts.URL + "/trips/" + idStr + "/filters")
	if err != nil {
		t.Fatal(err)
	}
	got := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200, body:\n%s", resp.StatusCode, got)
	}

	for _, want := range []string{
		`hx-post="/trips/` + idStr + `/filters/mode"`,
		`hx-post="/trips/` + idStr + `/filters/resorts/TST"`,
		`hx-post="/trips/` + idStr + `/filters/roomtypes/STUDIO"`,
		`hx-delete="/trips/` + idStr + `/filters"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected panel to contain %q, got:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Trip 1") || strings.Contains(got, "Trip 2") {
		t.Errorf("expected trip name, not positional numbering, got:\n%s", got)
	}
	if !strings.Contains(got, "Second trip") {
		t.Errorf("expected the trip's actual name in the panel, got:\n%s", got)
	}
}

// TestTripFilterMode_OverrideSeedsFromGlobal proves switching a trip to
// override copies the CURRENT global exclusion set onto the trip row (not an
// empty one), and that the trip's slice does not alias the global config's:
// mutating the global config afterward must not move the already-stored
// trip.
func TestTripFilterMode_OverrideSeedsFromGlobal(t *testing.T) {
	ts, store := newTripFiltersTestServer(t)
	defer ts.Close()
	ctx := context.Background()

	// Exclude TS2 globally before the trip exists.
	if resp, err := http.Post(ts.URL+"/filters/resorts/TS2", "", nil); err != nil {
		t.Fatal(err)
	} else {
		resp.Body.Close()
	}

	id, err := store.AddTrip(ctx, ledger.Trip{
		Name: "Seeded trip", StartDate: dateParse(t, "2026-01-05"), EndDate: dateParse(t, "2026-01-10"), MinNights: 2,
	})
	if err != nil {
		t.Fatalf("AddTrip: %v", err)
	}

	resp, err := http.PostForm(ts.URL+"/trips/"+strconv.FormatInt(id, 10)+"/filters/mode", url.Values{"mode": {"override"}})
	if err != nil {
		t.Fatal(err)
	}
	got := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("switch to override status = %d, want 200, body:\n%s", resp.StatusCode, got)
	}

	stored, err := store.GetTrip(ctx, id)
	if err != nil {
		t.Fatalf("GetTrip: %v", err)
	}
	if stored.FilterMode != ledger.TripFilterOverride {
		t.Fatalf("FilterMode = %q, want override", stored.FilterMode)
	}
	if len(stored.ExcludeResorts) != 1 || stored.ExcludeResorts[0] != "TS2" {
		t.Fatalf("ExcludeResorts = %v, want seeded [TS2]", stored.ExcludeResorts)
	}

	// Mutate the global config afterward (toggle TS2 back off globally) and
	// confirm the trip's already-stored set is unaffected: it aliases
	// nothing.
	if resp, err := http.Post(ts.URL+"/filters/resorts/TS2", "", nil); err != nil {
		t.Fatal(err)
	} else {
		resp.Body.Close()
	}
	afterGlobalChange, err := store.GetTrip(ctx, id)
	if err != nil {
		t.Fatalf("GetTrip after global change: %v", err)
	}
	if len(afterGlobalChange.ExcludeResorts) != 1 || afterGlobalChange.ExcludeResorts[0] != "TS2" {
		t.Errorf("ExcludeResorts after global mutation = %v, want still [TS2] (no aliasing)", afterGlobalChange.ExcludeResorts)
	}
}

// TestTripFilterMode_InheritClearsSet proves POST .../filters/mode
// mode=inherit clears both exclusion slices, not just FilterMode — the
// mode=inherit counterpart of TestTripFilterReset_ClearsModeAndSet. Without
// this, a trip switched inherit -> override -> inherit -> override again
// would resurrect the filters the user reset instead of re-seeding from the
// then-current global config.
func TestTripFilterMode_InheritClearsSet(t *testing.T) {
	ts, store := newTripFiltersTestServer(t)
	defer ts.Close()
	ctx := context.Background()

	id, err := store.AddTrip(ctx, ledger.Trip{
		Name: "Switch to inherit", StartDate: dateParse(t, "2026-01-05"), EndDate: dateParse(t, "2026-01-10"), MinNights: 2,
		FilterMode:       ledger.TripFilterOverride,
		ExcludeResorts:   []string{"TS2"},
		ExcludeRoomTypes: []string{"STUDIO"},
	})
	if err != nil {
		t.Fatalf("AddTrip: %v", err)
	}

	resp, err := http.PostForm(ts.URL+"/trips/"+strconv.FormatInt(id, 10)+"/filters/mode", url.Values{"mode": {"inherit"}})
	if err != nil {
		t.Fatal(err)
	}
	got := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("switch to inherit status = %d, want 200, body:\n%s", resp.StatusCode, got)
	}

	stored, err := store.GetTrip(ctx, id)
	if err != nil {
		t.Fatalf("GetTrip: %v", err)
	}
	if stored.FilterMode != ledger.TripFilterInherit {
		t.Errorf("FilterMode = %q, want inherit", stored.FilterMode)
	}
	if len(stored.ExcludeResorts) != 0 {
		t.Errorf("ExcludeResorts = %v, want empty", stored.ExcludeResorts)
	}
	if len(stored.ExcludeRoomTypes) != 0 {
		t.Errorf("ExcludeRoomTypes = %v, want empty", stored.ExcludeRoomTypes)
	}
}

// TestTripFilterToggle_PersistsOnTheTripRow toggles a resort exclusion on an
// override-mode trip and proves it lands on the trip ROW (store.GetTrip),
// and that a FRESH GET /trips/{id} (not just the toggle response) still
// reflects it in the trip's search results — the "reload — it persisted"
// check that used to fail when trip filters lived only in memory.
func TestTripFilterToggle_PersistsOnTheTripRow(t *testing.T) {
	ts, store := newTripFiltersTestServer(t)
	defer ts.Close()
	ctx := context.Background()

	id, err := store.AddTrip(ctx, ledger.Trip{
		Name: "Toggle trip", StartDate: dateParse(t, "2026-01-05"), EndDate: dateParse(t, "2026-01-20"), MinNights: 3,
		FilterMode: ledger.TripFilterOverride,
	})
	if err != nil {
		t.Fatalf("AddTrip: %v", err)
	}
	idStr := strconv.FormatInt(id, 10)

	resp, err := http.Post(ts.URL+"/trips/"+idStr+"/filters/resorts/TS2", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	got := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("toggle status = %d, want 200, body:\n%s", resp.StatusCode, got)
	}

	stored, err := store.GetTrip(ctx, id)
	if err != nil {
		t.Fatalf("GetTrip: %v", err)
	}
	if !strings.Contains(strings.Join(stored.ExcludeResorts, ","), "TS2") {
		t.Fatalf("stored ExcludeResorts = %v, want to contain TS2", stored.ExcludeResorts)
	}

	resp2, err := http.Get(ts.URL + "/trips/" + idStr)
	if err != nil {
		t.Fatal(err)
	}
	page := body(t, resp2)
	if strings.Contains(page, "Test Resort Two") {
		t.Errorf("fresh GET /trips/%s still shows the excluded resort, want it gone:\n%s", idStr, page)
	}
	if !strings.Contains(page, "Test Resort One") {
		t.Errorf("fresh GET /trips/%s missing the non-excluded resort:\n%s", idStr, page)
	}
}

// TestTripFilterToggle_InheritModeIsRejected proves a toggle request against
// an inherit-mode trip is rejected with 409, and leaves the stored trip
// unchanged — the template disables these buttons in inherit mode, so this
// exercises the server-side guard for an out-of-band request.
func TestTripFilterToggle_InheritModeIsRejected(t *testing.T) {
	ts, store := newTripFiltersTestServer(t)
	defer ts.Close()
	ctx := context.Background()

	id, err := store.AddTrip(ctx, ledger.Trip{
		Name: "Inherit trip", StartDate: dateParse(t, "2026-01-05"), EndDate: dateParse(t, "2026-01-10"), MinNights: 2,
	})
	if err != nil {
		t.Fatalf("AddTrip: %v", err)
	}

	resp, err := http.Post(ts.URL+"/trips/"+strconv.FormatInt(id, 10)+"/filters/resorts/TS2", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	got := body(t, resp)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409, body:\n%s", resp.StatusCode, got)
	}

	stored, err := store.GetTrip(ctx, id)
	if err != nil {
		t.Fatalf("GetTrip: %v", err)
	}
	if len(stored.ExcludeResorts) != 0 {
		t.Errorf("ExcludeResorts = %v, want unchanged/empty after rejected toggle", stored.ExcludeResorts)
	}
	if stored.FilterMode != ledger.TripFilterInherit {
		t.Errorf("FilterMode = %q, want unchanged inherit", stored.FilterMode)
	}
}

// TestTripFilterReset_ClearsModeAndSet proves DELETE /trips/{id}/filters
// resets FilterMode to inherit AND clears both exclusion slices — not just
// the mode — so a later switch back to override re-seeds from the
// then-current global config instead of resurrecting the reset filters.
func TestTripFilterReset_ClearsModeAndSet(t *testing.T) {
	ts, store := newTripFiltersTestServer(t)
	defer ts.Close()
	ctx := context.Background()

	id, err := store.AddTrip(ctx, ledger.Trip{
		Name: "Reset trip", StartDate: dateParse(t, "2026-01-05"), EndDate: dateParse(t, "2026-01-10"), MinNights: 2,
		FilterMode:       ledger.TripFilterOverride,
		ExcludeResorts:   []string{"TS2"},
		ExcludeRoomTypes: []string{"STUDIO"},
	})
	if err != nil {
		t.Fatalf("AddTrip: %v", err)
	}

	req, err := http.NewRequest(http.MethodDelete, ts.URL+"/trips/"+strconv.FormatInt(id, 10)+"/filters", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	got := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reset status = %d, want 200, body:\n%s", resp.StatusCode, got)
	}

	stored, err := store.GetTrip(ctx, id)
	if err != nil {
		t.Fatalf("GetTrip: %v", err)
	}
	if stored.FilterMode != ledger.TripFilterInherit {
		t.Errorf("FilterMode = %q, want inherit", stored.FilterMode)
	}
	if len(stored.ExcludeResorts) != 0 {
		t.Errorf("ExcludeResorts = %v, want empty", stored.ExcludeResorts)
	}
	if len(stored.ExcludeRoomTypes) != 0 {
		t.Errorf("ExcludeRoomTypes = %v, want empty", stored.ExcludeRoomTypes)
	}
}

// TestTripFilters_NarrowResultsForThisTripOnly is the point of the feature:
// two trips share the same window. Trip A overrides, excluding TS2; trip B
// inherits. A's results must exclude TS2 while B's still show it. Toggling
// TS2 globally afterward must narrow B (which inherits) while leaving A
// unaffected (it was already excluding TS2 independently, via its own
// override — not because the global toggle reached it).
func TestTripFilters_NarrowResultsForThisTripOnly(t *testing.T) {
	ts, store := newTripFiltersTestServer(t)
	defer ts.Close()
	ctx := context.Background()

	idA, err := store.AddTrip(ctx, ledger.Trip{
		Name: "Trip A (override)", StartDate: dateParse(t, "2026-01-05"), EndDate: dateParse(t, "2026-01-20"), MinNights: 3,
		FilterMode:     ledger.TripFilterOverride,
		ExcludeResorts: []string{"TS2"},
	})
	if err != nil {
		t.Fatalf("AddTrip A: %v", err)
	}
	idB, err := store.AddTrip(ctx, ledger.Trip{
		Name: "Trip B (inherit)", StartDate: dateParse(t, "2026-01-05"), EndDate: dateParse(t, "2026-01-20"), MinNights: 3,
	})
	if err != nil {
		t.Fatalf("AddTrip B: %v", err)
	}

	getTrip := func(id int64) string {
		t.Helper()
		resp, err := http.Get(ts.URL + "/trips/" + strconv.FormatInt(id, 10))
		if err != nil {
			t.Fatal(err)
		}
		return body(t, resp)
	}

	pageA := getTrip(idA)
	if strings.Contains(pageA, "Test Resort Two") {
		t.Errorf("trip A (override excluding TS2) shows Test Resort Two, want absent:\n%s", pageA)
	}
	if !strings.Contains(pageA, "Test Resort One") {
		t.Errorf("trip A missing Test Resort One:\n%s", pageA)
	}

	pageB := getTrip(idB)
	if !strings.Contains(pageB, "Test Resort Two") {
		t.Errorf("trip B (inherit, no global exclusions yet) missing Test Resort Two:\n%s", pageB)
	}

	// Now exclude TS2 globally.
	if resp, err := http.Post(ts.URL+"/filters/resorts/TS2", "", nil); err != nil {
		t.Fatal(err)
	} else {
		resp.Body.Close()
	}

	pageBAfter := getTrip(idB)
	if strings.Contains(pageBAfter, "Test Resort Two") {
		t.Errorf("trip B after global exclusion still shows Test Resort Two, want narrowed:\n%s", pageBAfter)
	}

	pageAAfter := getTrip(idA)
	if strings.Contains(pageAAfter, "Test Resort Two") {
		t.Errorf("trip A after global exclusion shows Test Resort Two, want still absent:\n%s", pageAAfter)
	}
	if !strings.Contains(pageAAfter, "Test Resort One") {
		t.Errorf("trip A after global exclusion missing Test Resort One (A should be unaffected by the global change):\n%s", pageAAfter)
	}
}

// TestTripFilters_UnknownTripIs404 proves every per-trip filter route 404s
// on an id that matches no trip, consistent with tripPage.
func TestTripFilters_UnknownTripIs404(t *testing.T) {
	ts, _ := newTripFiltersTestServer(t)
	defer ts.Close()

	const unknown = "999999"

	checks := []struct {
		method, path string
	}{
		{http.MethodGet, "/trips/" + unknown + "/filters"},
		{http.MethodPost, "/trips/" + unknown + "/filters/mode"},
		{http.MethodPost, "/trips/" + unknown + "/filters/resorts/TST"},
		{http.MethodPost, "/trips/" + unknown + "/filters/roomtypes/STUDIO"},
		{http.MethodDelete, "/trips/" + unknown + "/filters"},
	}
	for _, c := range checks {
		req, err := http.NewRequest(c.method, ts.URL+c.path, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s %s status = %d, want 404", c.method, c.path, resp.StatusCode)
		}
	}
}

// TestTripPage_OffersItsOwnFilterPanel proves the trip page has a UI entry
// point into GET /trips/{id}/filters.
//
// Without it every route in this file is dead code: the panel, the mode
// switch, the per-trip toggles and the reset are all reachable only from a
// panel the user has no way to open, and the trip page's only filter
// affordance would be the GLOBAL one inherited from the trip list. The
// assertion is deliberately specific about which URL the button targets,
// since a button pointing at "/filters" would look right on screen and
// silently edit every inheriting trip instead of this one.
func TestTripPage_OffersItsOwnFilterPanel(t *testing.T) {
	ts, store := newLedgerTestServer(t)
	defer ts.Close()

	addBudgetContract(t, store, 100, time.January)

	id := createTripViaForm(t, ts.URL, url.Values{
		"name":       {"Panel entry point"},
		"from":       {"2026-01-05"},
		"to":         {"2026-01-20"},
		"min_nights": {"3"},
	})

	page := body(t, httpDo(t, http.MethodGet, ts.URL+"/trips/"+strconv.FormatInt(id, 10)))

	want := `hx-get="/trips/` + strconv.FormatInt(id, 10) + `/filters"`
	if !strings.Contains(page, want) {
		t.Fatalf("trip page has no button opening its own filter panel (looked for %s) — the per-trip filter routes would be unreachable from the UI:\n%s", want, page)
	}
	if !strings.Contains(page, `hx-target="#panel"`) {
		t.Errorf(`trip page's filter button does not target #panel, so the panel would not render where trip_page puts it`)
	}
}
