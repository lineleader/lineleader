package web

import (
	"context"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lineleader/lineleader/internal/dvc"
	"github.com/lineleader/lineleader/internal/ledger"
)

// stayEndpoint builds the /trips/{id}/stays/{row} URL used by both addStay
// and removeStay.
func stayEndpoint(base string, tripID int64, seg string) string {
	return base + "/trips/" + strconv.FormatInt(tripID, 10) + "/stays/" + seg
}

// httpDo issues method against url with no body, returning the response.
func httpDo(t *testing.T, method, u string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, u, nil)
	if err != nil {
		t.Fatalf("building %s %s: %v", method, u, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, u, err)
	}
	return resp
}

// TestAddStay_PersistsTheSelectedResultRow proves POST
// /trips/{id}/stays/{row} reconstructs the trip's search results and
// persists results[row] verbatim as a new, unbooked TripStay.
func TestAddStay_PersistsTheSelectedResultRow(t *testing.T) {
	ts, store := newLedgerTestServer(t)
	defer ts.Close()
	ctx := context.Background()

	addBudgetContract(t, store, 100, time.January)

	id := createTripViaForm(t, ts.URL, url.Values{
		"name":       {"Add stay trip"},
		"from":       {"2026-01-05"},
		"to":         {"2026-01-20"},
		"min_nights": {"3"},
	})

	trip, err := store.GetTrip(ctx, id)
	if err != nil {
		t.Fatalf("GetTrip: %v", err)
	}
	budget, err := store.TripBudget(ctx, trip.StartDate)
	if err != nil {
		t.Fatalf("TripBudget: %v", err)
	}

	// Reconstruct the expected result set exactly the way addStay does, so
	// the assertion is against the real row rather than a hand-built guess.
	filters := dvc.EffectiveFilters(dvc.Config{}, dvc.FilterMode(trip.FilterMode), dvc.FilterSet{
		ExcludeResorts:   trip.ExcludeResorts,
		ExcludeRoomTypes: trip.ExcludeRoomTypes,
	})
	results := dvc.Search([]*dvc.ResortChart{minimalChart()}, dvc.SearchParams{
		WindowStart:      trip.StartDate,
		WindowEnd:        trip.EndDate,
		Budget:           budget.Total,
		MinNights:        trip.MinNights,
		ExcludeResorts:   filters.ExcludeResorts,
		ExcludeRoomTypes: filters.ExcludeRoomTypes,
	})
	if len(results) == 0 {
		t.Fatal("precondition: expected at least one search result")
	}
	want := results[0]

	resp := httpDo(t, http.MethodPost, stayEndpoint(ts.URL, id, "0"))
	got := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("addStay status = %d, want 200, body:\n%s", resp.StatusCode, got)
	}

	stays, err := store.ListStays(ctx, id)
	if err != nil {
		t.Fatalf("ListStays: %v", err)
	}
	if len(stays) != 1 {
		t.Fatalf("len(stays) = %d, want 1", len(stays))
	}
	st := stays[0]
	if st.Resort != want.Resort {
		t.Errorf("Resort = %q, want %q", st.Resort, want.Resort)
	}
	if st.RoomType != want.RoomType {
		t.Errorf("RoomType = %q, want %q", st.RoomType, want.RoomType)
	}
	if st.View != want.View {
		t.Errorf("View = %q, want %q", st.View, want.View)
	}
	if !st.CheckIn.Equal(want.CheckIn) {
		t.Errorf("CheckIn = %v, want %v", st.CheckIn, want.CheckIn)
	}
	if !st.CheckOut.Equal(want.CheckOut) {
		t.Errorf("CheckOut = %v, want %v", st.CheckOut, want.CheckOut)
	}
	if st.Nights != want.Nights {
		t.Errorf("Nights = %d, want %d", st.Nights, want.Nights)
	}
	if st.Points != want.Points {
		t.Errorf("Points = %d, want %d", st.Points, want.Points)
	}
	if st.EntryID != nil {
		t.Errorf("EntryID = %v, want nil (newly added stay is unbooked)", st.EntryID)
	}
	if st.TripID != id {
		t.Errorf("TripID = %d, want %d", st.TripID, id)
	}
}

// TestAddStay_NarrowsRemainingBudget proves the re-rendered #trip fragment's
// Remaining reflects the just-added stay's points coming off the search
// budget.
func TestAddStay_NarrowsRemainingBudget(t *testing.T) {
	ts, store := newLedgerTestServer(t)
	defer ts.Close()

	addBudgetContract(t, store, 100, time.January)

	id := createTripViaForm(t, ts.URL, url.Values{
		"name":       {"Narrow budget trip"},
		"from":       {"2026-01-05"},
		"to":         {"2026-01-20"},
		"min_nights": {"3"},
	})

	before, err := http.Get(ts.URL + "/trips/" + strconv.FormatInt(id, 10))
	if err != nil {
		t.Fatal(err)
	}
	beforeBody := body(t, before)
	beforeRemaining := extractRemaining(t, beforeBody)

	resp := httpDo(t, http.MethodPost, stayEndpoint(ts.URL, id, "0"))
	got := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("addStay status = %d, want 200, body:\n%s", resp.StatusCode, got)
	}
	afterRemaining := extractRemaining(t, got)

	stays, err := store.ListStays(context.Background(), id)
	if err != nil {
		t.Fatalf("ListStays: %v", err)
	}
	if len(stays) != 1 {
		t.Fatalf("len(stays) = %d, want 1", len(stays))
	}

	wantRemaining := beforeRemaining - stays[0].Points
	if afterRemaining != wantRemaining {
		t.Errorf("Remaining after add = %d, want %d (before %d - added stay's %d points)",
			afterRemaining, wantRemaining, beforeRemaining, stays[0].Points)
	}
}

// extractRemaining pulls the integer out of the budget-remaining row
// rendered by trip.html's trip_budget sub-template
// (<div class="budget-row budget-remaining"><dt>Remaining</dt><dd>N</dd></div>).
func extractRemaining(t *testing.T, html string) int {
	t.Helper()
	const marker = `budget-remaining"><dt>Remaining</dt><dd>`
	idx := strings.Index(html, marker)
	if idx < 0 {
		t.Fatalf("expected budget-remaining row in body, got:\n%s", html)
	}
	rest := html[idx+len(marker):]
	end := strings.Index(rest, "<")
	if end < 0 {
		t.Fatalf("malformed budget-remaining row in body, got:\n%s", html)
	}
	n, err := strconv.Atoi(strings.TrimSpace(rest[:end]))
	if err != nil {
		t.Fatalf("parsing Remaining value %q: %v", rest[:end], err)
	}
	return n
}

// TestAddStay_RowOutOfRange proves an out-of-range {row} 400s and creates no
// stay.
func TestAddStay_RowOutOfRange(t *testing.T) {
	ts, store := newLedgerTestServer(t)
	defer ts.Close()

	addBudgetContract(t, store, 100, time.January)

	id := createTripViaForm(t, ts.URL, url.Values{
		"name":       {"Out of range trip"},
		"from":       {"2026-01-05"},
		"to":         {"2026-01-20"},
		"min_nights": {"3"},
	})

	resp := httpDo(t, http.MethodPost, stayEndpoint(ts.URL, id, "999"))
	got := body(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("addStay (out of range) status = %d, want 400, body:\n%s", resp.StatusCode, got)
	}

	stays, err := store.ListStays(context.Background(), id)
	if err != nil {
		t.Fatalf("ListStays: %v", err)
	}
	if len(stays) != 0 {
		t.Errorf("len(stays) = %d, want 0 (no stay created on a rejected row)", len(stays))
	}
}

// TestAddStay_SecondStayReconstructsWithNarrowedBudget proves addStay
// reconstructs the results it resolves {row} against using searchBudgetFor
// (the effective budget less already-collected UNBOOKED stays' points), not
// the raw effectiveBudget. Adding only a FIRST stay can't distinguish these
// two — with zero prior stays they coincide — so this adds a first stay,
// then targets a row that is only in-range against the WIDE effectiveBudget
// result set, not the narrowed searchBudgetFor one.
//
// With a 100-point budget and this package's minimalChart (TST/STUDIO,
// Jan 2026, 10pts Sun-Thu / 14pts Fri-Sat), a 2026-01-05..2026-01-20 window
// at min_nights=3 yields 67 results at Budget=100, but only 46 once the
// first stay's 30 points are subtracted (Budget=70) — see this file's
// probe data. Row 50 is valid under the wide budget but out of range under
// the narrowed one: a correct addStay 400s and creates no second stay; a
// version that reconstructed against effectiveBudget would happily add a
// stay costing more than what's actually left.
func TestAddStay_SecondStayReconstructsWithNarrowedBudget(t *testing.T) {
	ts, store := newLedgerTestServer(t)
	defer ts.Close()
	ctx := context.Background()

	addBudgetContract(t, store, 100, time.January)

	id := createTripViaForm(t, ts.URL, url.Values{
		"name":       {"Narrowed reconstruction trip"},
		"from":       {"2026-01-05"},
		"to":         {"2026-01-20"},
		"min_nights": {"3"},
	})

	// First stay: row 0, 30 points (the cheapest option).
	firstResp := httpDo(t, http.MethodPost, stayEndpoint(ts.URL, id, "0"))
	firstBody := body(t, firstResp)
	if firstResp.StatusCode != http.StatusOK {
		t.Fatalf("first addStay status = %d, want 200, body:\n%s", firstResp.StatusCode, firstBody)
	}
	stays, err := store.ListStays(ctx, id)
	if err != nil {
		t.Fatalf("ListStays: %v", err)
	}
	if len(stays) != 1 {
		t.Fatalf("precondition: len(stays) = %d, want 1 after first addStay", len(stays))
	}
	if stays[0].Points != 30 {
		t.Fatalf("precondition: first stay Points = %d, want 30 (chart/budget assumptions changed — update this test's probe data)", stays[0].Points)
	}

	// Second addStay: row 50 is in range for the full 100-point budget (67
	// results) but out of range for the narrowed 70-point budget (46
	// results after subtracting the first stay's 30 points).
	secondResp := httpDo(t, http.MethodPost, stayEndpoint(ts.URL, id, "50"))
	secondBody := body(t, secondResp)
	if secondResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("second addStay (row 50) status = %d, want 400 (row must be resolved against the NARROWED searchBudgetFor budget, not the raw effectiveBudget), body:\n%s", secondResp.StatusCode, secondBody)
	}

	afterStays, err := store.ListStays(ctx, id)
	if err != nil {
		t.Fatalf("ListStays after rejected second add: %v", err)
	}
	if len(afterStays) != 1 {
		t.Errorf("len(stays) = %d, want 1 (rejected row must not create a second stay)", len(afterStays))
	}
}

// TestRemoveStay_DeletesTheStay proves DELETE /trips/{id}/stays/{sid}
// removes the stay and the re-rendered fragment no longer lists it.
func TestRemoveStay_DeletesTheStay(t *testing.T) {
	ts, store := newLedgerTestServer(t)
	defer ts.Close()
	ctx := context.Background()

	addBudgetContract(t, store, 100, time.January)

	id := createTripViaForm(t, ts.URL, url.Values{
		"name":       {"Remove stay trip"},
		"from":       {"2026-01-05"},
		"to":         {"2026-01-20"},
		"min_nights": {"3"},
	})

	addResp := httpDo(t, http.MethodPost, stayEndpoint(ts.URL, id, "0"))
	addResp.Body.Close()
	if addResp.StatusCode != http.StatusOK {
		t.Fatalf("addStay status = %d, want 200", addResp.StatusCode)
	}

	stays, err := store.ListStays(ctx, id)
	if err != nil {
		t.Fatalf("ListStays: %v", err)
	}
	if len(stays) != 1 {
		t.Fatalf("len(stays) = %d, want 1", len(stays))
	}
	sid := stays[0].ID

	delResp := httpDo(t, http.MethodDelete, ts.URL+"/trips/"+strconv.FormatInt(id, 10)+"/stays/"+strconv.FormatInt(sid, 10))
	got := body(t, delResp)
	if delResp.StatusCode != http.StatusOK {
		t.Fatalf("removeStay status = %d, want 200, body:\n%s", delResp.StatusCode, got)
	}

	remaining, err := store.ListStays(ctx, id)
	if err != nil {
		t.Fatalf("ListStays after delete: %v", err)
	}
	if len(remaining) != 0 {
		t.Errorf("len(stays) after delete = %d, want 0", len(remaining))
	}
}

// TestRemoveStay_BookedStayInvalidatesCostProvider is modelled on
// TestLedgerDuesMutationInvalidatesCostProvider: deleting a BOOKED stay also
// deletes the ledger entry it created (ledger.Store.DeleteStay), and that
// ledger mutation must invalidate the handlers' cached CostBasis — the easy
// thing to forget. It exercises the handlers struct directly (not through
// NewServer/httptest), same as TestLedgerDuesMutationInvalidatesCostProvider,
// so it can inspect costs.Basis() itself and drive removeStay's PathValues
// without a real mux.
func TestRemoveStay_BookedStayInvalidatesCostProvider(t *testing.T) {
	store := ledger.OpenTest(t)
	ctx := context.Background()

	// A priced contract, so CostBasis.Known() is true — seed.sql stores
	// dues rates back to 2019, including 2026.
	if _, err := store.AddContract(ctx, ledger.Contract{
		Name:          "C1",
		AnnualPoints:  100,
		UseYearMonth:  time.January,
		TermYears:     10,
		PurchasePrice: 100_000_00,
	}); err != nil {
		t.Fatalf("AddContract: %v", err)
	}

	id, err := store.AddTrip(ctx, ledger.Trip{
		Name:      "Booked stay trip",
		StartDate: dateParse(t, "2026-01-05"),
		EndDate:   dateParse(t, "2026-01-20"),
		MinNights: 3,
	})
	if err != nil {
		t.Fatalf("AddTrip: %v", err)
	}
	sid, err := store.AddStay(ctx, ledger.TripStay{
		TripID:   id,
		Resort:   "Test Resort",
		RoomType: "STUDIO",
		CheckIn:  dateParse(t, "2026-01-05"),
		CheckOut: dateParse(t, "2026-01-08"),
		Nights:   3,
		Points:   30,
	})
	if err != nil {
		t.Fatalf("AddStay: %v", err)
	}
	if err := store.BookTrip(ctx, id); err != nil {
		t.Fatalf("BookTrip: %v", err)
	}

	costs := newCostProvider(store)
	basis1, ok := costs.Basis(ctx)
	if !ok || !basis1.Known() {
		t.Fatalf("Basis() = (%+v, %v), want a known basis", basis1, ok)
	}
	rate1, _ := basis1.DuesFor(2026)
	if rate1 != 8_223_500 {
		t.Fatalf("seeded 2026 dues rate = %v, want 8_223_500 ($8.2235, per seed.sql)", rate1)
	}

	tmpl := template.Must(template.New("").Funcs(templateFuncs()).ParseFS(templatesFS, "templates/*.html"))
	dir := t.TempDir()
	h := &handlers{
		tmpl:   tmpl,
		charts: []*dvc.ResortChart{minimalChart()},
		global: newGlobalFilters(dvc.Config{}, filepath.Join(dir, "config.json")),
		store:  store,
		costs:  costs,
	}

	// Prime the cache again so the mutation below has something stale to
	// invalidate (Basis() above already cached it, but be explicit — same
	// pattern as TestLedgerDuesMutationInvalidatesCostProvider).
	costs.Basis(ctx)

	// Mutate dues directly through the store (bypassing the ledger
	// handlers, which are not under test here), then delete the booked
	// stay through the removeStay handler under test.
	if err := store.UpsertDuesRate(ctx, ledger.DuesRate{UseYear: 2026, Rate: 20_000_000}); err != nil {
		t.Fatalf("UpsertDuesRate: %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/trips/"+strconv.FormatInt(id, 10)+"/stays/"+strconv.FormatInt(sid, 10), nil)
	req.SetPathValue("id", strconv.FormatInt(id, 10))
	req.SetPathValue("sid", strconv.FormatInt(sid, 10))
	w := httptest.NewRecorder()
	h.removeStay(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("removeStay status = %d, body = %s", w.Code, w.Body.String())
	}

	basis2, ok := costs.Basis(ctx)
	if !ok {
		t.Fatalf("Basis() after removeStay: ok = false, want true")
	}
	rate2, _ := basis2.DuesFor(2026)
	if rate2 != 20_000_000 {
		t.Errorf("2026 dues rate after removeStay = %v, want 20_000_000 ($20.00) — removeStay did not call h.costs.Invalidate(), or Basis() served a stale cache", rate2)
	}
}

// TestTripPage_RenderedRowsAreAllAddable pins the OTHER half of the
// row-index invariant. TestAddStay_SecondStayReconstructsWithNarrowedBudget
// proves addStay resolves {row} against the NARROWED budget; this proves the
// page RENDERS that same narrowed result set.
//
// Both halves are needed, and neither implies the other. If
// buildTripPageView searched at the raw effectiveBudget while addStay
// reconstructed at searchBudgetFor, every assertion in this file would still
// pass — yet the page would show result rows that addStay rejects with a
// 400, and (worse) the row a user clicks would index into a different list
// than the one they are looking at. That desync is exactly what searchTrip's
// determinism comment promises cannot happen, so it gets a test rather than
// only a comment.
//
// The assertion is deliberately shaped as "the last row the page offers is
// addable" rather than a hardcoded row count: it survives chart changes, and
// it is the user-facing property that actually matters.
func TestTripPage_RenderedRowsAreAllAddable(t *testing.T) {
	ts, store := newLedgerTestServer(t)
	defer ts.Close()
	ctx := context.Background()

	addBudgetContract(t, store, 100, time.January)

	id := createTripViaForm(t, ts.URL, url.Values{
		"name":       {"Rendered rows addable"},
		"from":       {"2026-01-05"},
		"to":         {"2026-01-20"},
		"min_nights": {"3"},
	})

	// Collect one stay so the narrowed and un-narrowed budgets differ; with
	// zero stays searchBudgetFor and effectiveBudget coincide and this test
	// would prove nothing.
	if resp := httpDo(t, http.MethodPost, stayEndpoint(ts.URL, id, "0")); resp.StatusCode != http.StatusOK {
		t.Fatalf("seeding first stay: status = %d, want 200, body:\n%s", resp.StatusCode, body(t, resp))
	}
	stays, err := store.ListStays(ctx, id)
	if err != nil {
		t.Fatalf("ListStays: %v", err)
	}
	if len(stays) != 1 || stays[0].Points <= 0 {
		t.Fatalf("precondition: stays = %+v, want exactly one stay with positive points", stays)
	}

	page := body(t, httpDo(t, http.MethodGet, ts.URL+"/trips/"+strconv.FormatInt(id, 10)))
	last := lastRenderedStayRow(t, page)

	resp := httpDo(t, http.MethodPost, stayEndpoint(ts.URL, id, strconv.Itoa(last)))
	got := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("addStay for the LAST rendered row (%d) status = %d, want 200 — the page rendered a result row addStay rejects, so the two searches ran against different budgets, body:\n%s",
			last, resp.StatusCode, got)
	}
}

// lastRenderedStayRow returns the highest {row} index the page's result
// table offers via its select buttons' hx-post URLs, failing the test if the
// page rendered no addable rows at all.
func lastRenderedStayRow(t *testing.T, page string) int {
	t.Helper()
	re := regexp.MustCompile(`/stays/(\d+)"`)
	matches := re.FindAllStringSubmatch(page, -1)
	last := -1
	for _, m := range matches {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			t.Fatalf("parsing rendered row index %q: %v", m[1], err)
		}
		if n > last {
			last = n
		}
	}
	if last < 0 {
		t.Fatalf("page rendered no /stays/{row} select buttons; this test needs a non-empty result table")
	}
	return last
}
