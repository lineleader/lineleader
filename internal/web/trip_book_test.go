package web

import (
	"context"
	"fmt"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lineleader/lineleader/internal/dvc"
	"github.com/lineleader/lineleader/internal/ledger"
)

// bookEndpoint and unbookEndpoint build the /trips/{id}/book and
// /trips/{id}/unbook URLs, mirroring stayEndpoint's shape in
// trip_search_test.go.
func bookEndpoint(base string, tripID int64) string {
	return base + "/trips/" + strconv.FormatInt(tripID, 10) + "/book"
}

func unbookEndpoint(base string, tripID int64) string {
	return base + "/trips/" + strconv.FormatInt(tripID, 10) + "/unbook"
}

// findEntriesFor filters entries down to those matching both wantDesc and
// wantDate — internal/web can't import internal/ledger's test-only
// findEntryByDesc helper, so this is its tiny local counterpart. Matching on
// Desc alone is not enough here: minimalChart has a single resort/room
// type, so two stays checking in on DIFFERENT dates still share the exact
// same Desc string ("<trip name> — <resort> <room type>") — the date is
// what tells their entries apart.
func findEntriesFor(entries []ledger.Entry, wantDesc string, wantDate time.Time) []ledger.Entry {
	var out []ledger.Entry
	for _, e := range entries {
		if e.Desc == wantDesc && e.Date.Equal(wantDate) {
			out = append(out, e)
		}
	}
	return out
}

// TestBookTrip_DoesNotChangeRemainingBudget pins the key invariant behind
// ixe.14: booking a trip must not move the displayed Remaining points. A
// booked stay's points are already reflected in the ledger's used(UY),
// which reduces Budget.Current; searchBudgetFor stops subtracting an
// unbooked stay's points locally the moment it becomes booked, so the two
// effects must cancel exactly (see searchBudgetFor's doc comment in
// render.go).
//
// This invariant only holds when every stay's check-in falls in the trip's
// DISPLAYED use year (the window start's use year) — a window that spans
// use years (SpansUseYears/SpanNote) legitimately shifts which use year's
// Remaining is shown, because a stay booked into the LATER use year moves
// its points out of the displayed year's local subtraction without adding
// anything back to the displayed year's used(UY). This test deliberately
// avoids that case: the trip window (2026-01-05..2026-01-20) sits entirely
// inside a single calendar year against a January-anchored contract, so
// SpansUseYears is false and every stay's check-in is in the same use year
// as the window start.
func TestBookTrip_DoesNotChangeRemainingBudget(t *testing.T) {
	ts, store := newLedgerTestServer(t)
	defer ts.Close()
	ctx := context.Background()

	addBudgetContract(t, store, 100, time.January)

	id := createTripViaForm(t, ts.URL, url.Values{
		"name":       {"Book invariant trip"},
		"from":       {"2026-01-05"},
		"to":         {"2026-01-20"},
		"min_nights": {"3"},
	})

	// Collect two stays so the test exercises more than the trivial
	// single-stay case.
	for _, row := range []string{"0", "1"} {
		resp := httpDo(t, http.MethodPost, stayEndpoint(ts.URL, id, row))
		got := body(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("addStay(row=%s) status = %d, want 200, body:\n%s", row, resp.StatusCode, got)
		}
	}

	before := body(t, httpDo(t, http.MethodGet, ts.URL+"/trips/"+strconv.FormatInt(id, 10)))
	beforeRemaining := extractRemaining(t, before)

	entriesBefore, err := store.ListEntries(ctx)
	if err != nil {
		t.Fatalf("ListEntries (before): %v", err)
	}

	resp := httpDo(t, http.MethodPost, bookEndpoint(ts.URL, id))
	got := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("bookTrip status = %d, want 200, body:\n%s", resp.StatusCode, got)
	}
	afterRemaining := extractRemaining(t, got)

	if afterRemaining != beforeRemaining {
		t.Errorf("Remaining after book = %d, want %d (unchanged) — booking must not move Remaining, only shift where the points are accounted for", afterRemaining, beforeRemaining)
	}

	// "Remaining is unchanged" is satisfied trivially by a handler that
	// books and then renders the view it built BEFORE booking — the
	// pre-book Remaining is equal to the post-book one by definition. So
	// require the SAME response body to also show the booking took effect.
	// Without this, the invariant assertion above is vacuous.
	if !strings.Contains(got, `hx-post="/trips/`+strconv.FormatInt(id, 10)+`/unbook"`) {
		t.Errorf("book response body does not offer Unbook — it rendered a view built before the booking was applied, which would make the Remaining assertion above vacuous:\n%s", got)
	}
	if strings.Contains(got, `hx-post="/trips/`+strconv.FormatInt(id, 10)+`/book"`) {
		t.Errorf("book response body still offers 'Book it' after booking every stay — the rendered view predates the booking:\n%s", got)
	}

	stays, err := store.ListStays(ctx, id)
	if err != nil {
		t.Fatalf("ListStays: %v", err)
	}
	if len(stays) != 2 {
		t.Fatalf("len(stays) = %d, want 2", len(stays))
	}
	for _, st := range stays {
		if st.EntryID == nil {
			t.Errorf("stay %d EntryID = nil, want non-nil after booking", st.ID)
		}
	}

	entriesAfter, err := store.ListEntries(ctx)
	if err != nil {
		t.Fatalf("ListEntries (after): %v", err)
	}
	if len(entriesAfter) != len(entriesBefore)+2 {
		t.Errorf("len(entries) after book = %d, want %d (2 new usage entries)", len(entriesAfter), len(entriesBefore)+2)
	}
}

// TestBookTrip_WritesOneEntryPerStay proves BookTrip's ledger writes reach
// the trip page's web wiring correctly: one usage entry per stay, dated and
// described exactly as internal/ledger/trip_book.go's BookTrip promises.
func TestBookTrip_WritesOneEntryPerStay(t *testing.T) {
	ts, store := newLedgerTestServer(t)
	defer ts.Close()
	ctx := context.Background()

	addBudgetContract(t, store, 100, time.January)

	id := createTripViaForm(t, ts.URL, url.Values{
		"name":       {"Two stays trip"},
		"from":       {"2026-01-05"},
		"to":         {"2026-01-20"},
		"min_nights": {"3"},
	})

	for _, row := range []string{"0", "1"} {
		resp := httpDo(t, http.MethodPost, stayEndpoint(ts.URL, id, row))
		got := body(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("addStay(row=%s) status = %d, want 200, body:\n%s", row, resp.StatusCode, got)
		}
	}

	trip, err := store.GetTrip(ctx, id)
	if err != nil {
		t.Fatalf("GetTrip: %v", err)
	}
	stays, err := store.ListStays(ctx, id)
	if err != nil {
		t.Fatalf("ListStays: %v", err)
	}
	if len(stays) != 2 {
		t.Fatalf("precondition: len(stays) = %d, want 2", len(stays))
	}

	resp := httpDo(t, http.MethodPost, bookEndpoint(ts.URL, id))
	got := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("bookTrip status = %d, want 200, body:\n%s", resp.StatusCode, got)
	}

	entries, err := store.ListEntries(ctx)
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}

	// No contract has UseYearMonth set beyond addBudgetContract's own
	// January default, so UseYearStartMonth resolves to January here too.
	const month = time.January

	for _, st := range stays {
		wantDesc := trip.Name + " — " + st.Resort + " " + st.RoomType
		matches := findEntriesFor(entries, wantDesc, st.CheckIn)
		if len(matches) != 1 {
			t.Fatalf("entries matching desc %q and date %v = %d, want 1 (entries: %+v)", wantDesc, st.CheckIn, len(matches), entries)
		}
		e := matches[0]
		if !e.Date.Equal(st.CheckIn) {
			t.Errorf("entry Date = %v, want stay CheckIn %v", e.Date, st.CheckIn)
		}
		if e.Used != st.Points {
			t.Errorf("entry Used = %d, want stay Points %d", e.Used, st.Points)
		}
		if e.Kind != ledger.KindUsage {
			t.Errorf("entry Kind = %q, want %q", e.Kind, ledger.KindUsage)
		}
		wantUseYear := ledger.UseYearForDate(st.CheckIn, month)
		if e.UseYear != wantUseYear {
			t.Errorf("entry UseYear = %d, want %d", e.UseYear, wantUseYear)
		}
	}
}

// TestBookTrip_IsIdempotent proves a double-submitted book request (e.g. a
// retried form) is safe: the second call still 200s, creates no additional
// entries, and doesn't rewrite the stays' entry links.
func TestBookTrip_IsIdempotent(t *testing.T) {
	ts, store := newLedgerTestServer(t)
	defer ts.Close()
	ctx := context.Background()

	addBudgetContract(t, store, 100, time.January)

	id := createTripViaForm(t, ts.URL, url.Values{
		"name":       {"Idempotent book trip"},
		"from":       {"2026-01-05"},
		"to":         {"2026-01-20"},
		"min_nights": {"3"},
	})

	if resp := httpDo(t, http.MethodPost, stayEndpoint(ts.URL, id, "0")); resp.StatusCode != http.StatusOK {
		t.Fatalf("addStay status = %d, want 200, body:\n%s", resp.StatusCode, body(t, resp))
	}

	first := httpDo(t, http.MethodPost, bookEndpoint(ts.URL, id))
	firstBody := body(t, first)
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first bookTrip status = %d, want 200, body:\n%s", first.StatusCode, firstBody)
	}

	staysAfterFirst, err := store.ListStays(ctx, id)
	if err != nil {
		t.Fatalf("ListStays (after first book): %v", err)
	}
	if len(staysAfterFirst) != 1 || staysAfterFirst[0].EntryID == nil {
		t.Fatalf("precondition: stay must be booked after first book: %+v", staysAfterFirst)
	}
	wantEntryID := *staysAfterFirst[0].EntryID

	entriesAfterFirst, err := store.ListEntries(ctx)
	if err != nil {
		t.Fatalf("ListEntries (after first book): %v", err)
	}

	second := httpDo(t, http.MethodPost, bookEndpoint(ts.URL, id))
	secondBody := body(t, second)
	if second.StatusCode != http.StatusOK {
		t.Fatalf("second bookTrip status = %d, want 200, body:\n%s", second.StatusCode, secondBody)
	}

	entriesAfterSecond, err := store.ListEntries(ctx)
	if err != nil {
		t.Fatalf("ListEntries (after second book): %v", err)
	}
	if len(entriesAfterSecond) != len(entriesAfterFirst) {
		t.Errorf("len(entries) after second book = %d, want %d (unchanged — no additional entries)", len(entriesAfterSecond), len(entriesAfterFirst))
	}

	staysAfterSecond, err := store.ListStays(ctx, id)
	if err != nil {
		t.Fatalf("ListStays (after second book): %v", err)
	}
	if len(staysAfterSecond) != 1 || staysAfterSecond[0].EntryID == nil {
		t.Fatalf("stay must remain booked after second book: %+v", staysAfterSecond)
	}
	if *staysAfterSecond[0].EntryID != wantEntryID {
		t.Errorf("EntryID after second book = %d, want %d (unchanged — re-booking must not rewrite the link)", *staysAfterSecond[0].EntryID, wantEntryID)
	}
}

// TestUnbookTrip_RemovesTheEntriesAndClearsTheLinks proves POST
// /trips/{id}/unbook deletes the ledger entries this trip's stays created
// and clears their EntryID links, and that Remaining round-trips back to
// what it was before the book+unbook pair.
func TestUnbookTrip_RemovesTheEntriesAndClearsTheLinks(t *testing.T) {
	ts, store := newLedgerTestServer(t)
	defer ts.Close()
	ctx := context.Background()

	addBudgetContract(t, store, 100, time.January)

	id := createTripViaForm(t, ts.URL, url.Values{
		"name":       {"Unbook trip"},
		"from":       {"2026-01-05"},
		"to":         {"2026-01-20"},
		"min_nights": {"3"},
	})

	for _, row := range []string{"0", "1"} {
		if resp := httpDo(t, http.MethodPost, stayEndpoint(ts.URL, id, row)); resp.StatusCode != http.StatusOK {
			t.Fatalf("addStay(row=%s) status = %d, want 200, body:\n%s", row, resp.StatusCode, body(t, resp))
		}
	}

	preBook := body(t, httpDo(t, http.MethodGet, ts.URL+"/trips/"+strconv.FormatInt(id, 10)))
	preBookRemaining := extractRemaining(t, preBook)

	bookResp := httpDo(t, http.MethodPost, bookEndpoint(ts.URL, id))
	if bookResp.StatusCode != http.StatusOK {
		t.Fatalf("bookTrip status = %d, want 200, body:\n%s", bookResp.StatusCode, body(t, bookResp))
	} else {
		body(t, bookResp) // drain/close
	}

	staysBooked, err := store.ListStays(ctx, id)
	if err != nil {
		t.Fatalf("ListStays (booked): %v", err)
	}
	if len(staysBooked) != 2 {
		t.Fatalf("precondition: len(stays) = %d, want 2", len(staysBooked))
	}
	var bookedEntryIDs []int64
	for _, st := range staysBooked {
		if st.EntryID == nil {
			t.Fatalf("precondition: stay %d not booked: %+v", st.ID, st)
		}
		bookedEntryIDs = append(bookedEntryIDs, *st.EntryID)
	}

	unbookResp := httpDo(t, http.MethodPost, unbookEndpoint(ts.URL, id))
	unbookBody := body(t, unbookResp)
	if unbookResp.StatusCode != http.StatusOK {
		t.Fatalf("unbookTrip status = %d, want 200, body:\n%s", unbookResp.StatusCode, unbookBody)
	}
	postUnbookRemaining := extractRemaining(t, unbookBody)

	if postUnbookRemaining != preBookRemaining {
		t.Errorf("Remaining after unbook = %d, want %d (pre-book value — a book+unbook round trip must not drift Remaining)", postUnbookRemaining, preBookRemaining)
	}

	staysUnbooked, err := store.ListStays(ctx, id)
	if err != nil {
		t.Fatalf("ListStays (unbooked): %v", err)
	}
	if len(staysUnbooked) != 2 {
		t.Fatalf("len(stays) after unbook = %d, want 2 (unbook removes ledger entries, not stays)", len(staysUnbooked))
	}
	for _, st := range staysUnbooked {
		if st.EntryID != nil {
			t.Errorf("stay %d EntryID = %v, want nil after unbook", st.ID, *st.EntryID)
		}
	}

	entries, err := store.ListEntries(ctx)
	if err != nil {
		t.Fatalf("ListEntries (after unbook): %v", err)
	}
	// Scope the assertion to the entries THIS trip created, in case the
	// ledger carries other, unrelated entries.
	for _, eid := range bookedEntryIDs {
		for _, e := range entries {
			if e.ID == eid {
				t.Errorf("entry %d still present after unbook, want deleted", eid)
			}
		}
	}
}

// TestBookAndUnbook_InvalidateTheCostProvider is modelled on
// TestRemoveStay_BookedStayInvalidatesCostProvider in trip_stays_test.go:
// booking and unbooking both mutate the ledger, and either mutation must
// invalidate the handlers' cached CostBasis so a subsequent Basis() call
// reflects a dues-rate change made in between — the easy thing to forget,
// since the store call succeeding says nothing about the cache.
func TestBookAndUnbook_InvalidateTheCostProvider(t *testing.T) {
	newHandlers := func(t *testing.T, store *ledger.Store) (*handlers, *costProvider) {
		t.Helper()
		costs := newCostProvider(store)
		tmpl := template.Must(template.New("").Funcs(templateFuncs()).ParseFS(templatesFS, "templates/*.html"))
		dir := t.TempDir()
		h := &handlers{
			tmpl:   tmpl,
			charts: []*dvc.ResortChart{minimalChart()},
			global: newGlobalFilters(dvc.Config{}, filepath.Join(dir, "config.json")),
			store:  store,
			costs:  costs,
		}
		return h, costs
	}

	t.Run("book", func(t *testing.T) {
		store := ledger.OpenTest(t)
		ctx := context.Background()

		if _, err := store.AddContract(ctx, ledger.Contract{
			Name: "C1", AnnualPoints: 100, UseYearMonth: time.January, TermYears: 10, PurchasePrice: 100_000_00,
		}); err != nil {
			t.Fatalf("AddContract: %v", err)
		}
		id, err := store.AddTrip(ctx, ledger.Trip{
			Name:      "Book invalidate trip",
			StartDate: dateParse(t, "2026-01-05"),
			EndDate:   dateParse(t, "2026-01-20"),
			MinNights: 3,
		})
		if err != nil {
			t.Fatalf("AddTrip: %v", err)
		}
		if _, err := store.AddStay(ctx, ledger.TripStay{
			TripID: id, Resort: "Test Resort", RoomType: "STUDIO",
			CheckIn: dateParse(t, "2026-01-05"), CheckOut: dateParse(t, "2026-01-08"),
			Nights: 3, Points: 30,
		}); err != nil {
			t.Fatalf("AddStay: %v", err)
		}

		h, costs := newHandlers(t, store)

		basis1, ok := costs.Basis(ctx)
		if !ok || !basis1.Known() {
			t.Fatalf("Basis() = (%+v, %v), want a known basis", basis1, ok)
		}
		rate1, _ := basis1.DuesFor(2026)
		if rate1 != 8_223_500 {
			t.Fatalf("seeded 2026 dues rate = %v, want 8_223_500", rate1)
		}
		costs.Basis(ctx) // prime the cache again, matching the pattern in trip_stays_test.go

		if err := store.UpsertDuesRate(ctx, ledger.DuesRate{UseYear: 2026, Rate: 20_000_000}); err != nil {
			t.Fatalf("UpsertDuesRate: %v", err)
		}

		req := httptest.NewRequest(http.MethodPost, "/trips/"+strconv.FormatInt(id, 10)+"/book", nil)
		req.SetPathValue("id", strconv.FormatInt(id, 10))
		w := httptest.NewRecorder()
		h.bookTrip(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("bookTrip status = %d, body = %s", w.Code, w.Body.String())
		}

		basis2, ok := costs.Basis(ctx)
		if !ok {
			t.Fatalf("Basis() after bookTrip: ok = false, want true")
		}
		rate2, _ := basis2.DuesFor(2026)
		if rate2 != 20_000_000 {
			t.Errorf("2026 dues rate after bookTrip = %v, want 20_000_000 — bookTrip did not call h.costs.Invalidate(), or Basis() served a stale cache", rate2)
		}
	})

	t.Run("unbook", func(t *testing.T) {
		store := ledger.OpenTest(t)
		ctx := context.Background()

		if _, err := store.AddContract(ctx, ledger.Contract{
			Name: "C1", AnnualPoints: 100, UseYearMonth: time.January, TermYears: 10, PurchasePrice: 100_000_00,
		}); err != nil {
			t.Fatalf("AddContract: %v", err)
		}
		id, err := store.AddTrip(ctx, ledger.Trip{
			Name:      "Unbook invalidate trip",
			StartDate: dateParse(t, "2026-01-05"),
			EndDate:   dateParse(t, "2026-01-20"),
			MinNights: 3,
		})
		if err != nil {
			t.Fatalf("AddTrip: %v", err)
		}
		if _, err := store.AddStay(ctx, ledger.TripStay{
			TripID: id, Resort: "Test Resort", RoomType: "STUDIO",
			CheckIn: dateParse(t, "2026-01-05"), CheckOut: dateParse(t, "2026-01-08"),
			Nights: 3, Points: 30,
		}); err != nil {
			t.Fatalf("AddStay: %v", err)
		}
		if err := store.BookTrip(ctx, id); err != nil {
			t.Fatalf("BookTrip: %v", err)
		}

		h, costs := newHandlers(t, store)

		basis1, ok := costs.Basis(ctx)
		if !ok || !basis1.Known() {
			t.Fatalf("Basis() = (%+v, %v), want a known basis", basis1, ok)
		}
		costs.Basis(ctx) // prime the cache again

		if err := store.UpsertDuesRate(ctx, ledger.DuesRate{UseYear: 2026, Rate: 20_000_000}); err != nil {
			t.Fatalf("UpsertDuesRate: %v", err)
		}

		req := httptest.NewRequest(http.MethodPost, "/trips/"+strconv.FormatInt(id, 10)+"/unbook", nil)
		req.SetPathValue("id", strconv.FormatInt(id, 10))
		w := httptest.NewRecorder()
		h.unbookTrip(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("unbookTrip status = %d, body = %s", w.Code, w.Body.String())
		}

		basis2, ok := costs.Basis(ctx)
		if !ok {
			t.Fatalf("Basis() after unbookTrip: ok = false, want true")
		}
		rate2, _ := basis2.DuesFor(2026)
		if rate2 != 20_000_000 {
			t.Errorf("2026 dues rate after unbookTrip = %v, want 20_000_000 — unbookTrip did not call h.costs.Invalidate(), or Basis() served a stale cache", rate2)
		}
	})
}

// TestTripPage_BookControlsReflectDerivedStatus is table-driven over a
// trip's booking state and asserts the "Book it"/"Unbook" buttons render
// exactly when HasUnbookedStays/HasBookedStays say they should (see
// render.go's book_controls-driving fields).
func TestTripPage_BookControlsReflectDerivedStatus(t *testing.T) {
	const bookMarker = `hx-post="/trips/%d/book"`
	const unbookMarker = `hx-post="/trips/%d/unbook"`

	t.Run("no stays", func(t *testing.T) {
		ts, store := newLedgerTestServer(t)
		defer ts.Close()
		addBudgetContract(t, store, 100, time.January)
		id := createTripViaForm(t, ts.URL, url.Values{
			"name": {"No stays trip"}, "from": {"2026-01-05"}, "to": {"2026-01-20"}, "min_nights": {"3"},
		})
		page := body(t, httpDo(t, http.MethodGet, ts.URL+"/trips/"+strconv.FormatInt(id, 10)))
		if strings.Contains(page, fmt.Sprintf(bookMarker, id)) {
			t.Errorf("expected no Book it button for a trip with zero stays, got:\n%s", page)
		}
		if strings.Contains(page, fmt.Sprintf(unbookMarker, id)) {
			t.Errorf("expected no Unbook button for a trip with zero stays, got:\n%s", page)
		}
	})

	t.Run("all unbooked", func(t *testing.T) {
		ts, store := newLedgerTestServer(t)
		defer ts.Close()
		addBudgetContract(t, store, 100, time.January)
		id := createTripViaForm(t, ts.URL, url.Values{
			"name": {"All unbooked trip"}, "from": {"2026-01-05"}, "to": {"2026-01-20"}, "min_nights": {"3"},
		})
		if resp := httpDo(t, http.MethodPost, stayEndpoint(ts.URL, id, "0")); resp.StatusCode != http.StatusOK {
			t.Fatalf("addStay status = %d, want 200, body:\n%s", resp.StatusCode, body(t, resp))
		}
		page := body(t, httpDo(t, http.MethodGet, ts.URL+"/trips/"+strconv.FormatInt(id, 10)))
		if !strings.Contains(page, fmt.Sprintf(bookMarker, id)) {
			t.Errorf("expected a Book it button for a trip with only unbooked stays, got:\n%s", page)
		}
		if strings.Contains(page, fmt.Sprintf(unbookMarker, id)) {
			t.Errorf("expected no Unbook button for a trip with only unbooked stays, got:\n%s", page)
		}
	})

	t.Run("all booked", func(t *testing.T) {
		ts, store := newLedgerTestServer(t)
		defer ts.Close()
		addBudgetContract(t, store, 100, time.January)
		id := createTripViaForm(t, ts.URL, url.Values{
			"name": {"All booked trip"}, "from": {"2026-01-05"}, "to": {"2026-01-20"}, "min_nights": {"3"},
		})
		if resp := httpDo(t, http.MethodPost, stayEndpoint(ts.URL, id, "0")); resp.StatusCode != http.StatusOK {
			t.Fatalf("addStay status = %d, want 200, body:\n%s", resp.StatusCode, body(t, resp))
		}
		if resp := httpDo(t, http.MethodPost, bookEndpoint(ts.URL, id)); resp.StatusCode != http.StatusOK {
			t.Fatalf("bookTrip status = %d, want 200, body:\n%s", resp.StatusCode, body(t, resp))
		} else {
			body(t, resp)
		}
		page := body(t, httpDo(t, http.MethodGet, ts.URL+"/trips/"+strconv.FormatInt(id, 10)))
		if strings.Contains(page, fmt.Sprintf(bookMarker, id)) {
			t.Errorf("expected no Book it button for a fully booked trip, got:\n%s", page)
		}
		if !strings.Contains(page, fmt.Sprintf(unbookMarker, id)) {
			t.Errorf("expected an Unbook button for a fully booked trip, got:\n%s", page)
		}
	})

	t.Run("mixed", func(t *testing.T) {
		ts, store := newLedgerTestServer(t)
		defer ts.Close()
		addBudgetContract(t, store, 100, time.January)
		id := createTripViaForm(t, ts.URL, url.Values{
			"name": {"Mixed trip"}, "from": {"2026-01-05"}, "to": {"2026-01-20"}, "min_nights": {"3"},
		})
		if resp := httpDo(t, http.MethodPost, stayEndpoint(ts.URL, id, "0")); resp.StatusCode != http.StatusOK {
			t.Fatalf("addStay status = %d, want 200, body:\n%s", resp.StatusCode, body(t, resp))
		}
		if resp := httpDo(t, http.MethodPost, bookEndpoint(ts.URL, id)); resp.StatusCode != http.StatusOK {
			t.Fatalf("bookTrip status = %d, want 200, body:\n%s", resp.StatusCode, body(t, resp))
		} else {
			body(t, resp)
		}
		// Add a second, still-unbooked stay so the trip is genuinely mixed.
		if resp := httpDo(t, http.MethodPost, stayEndpoint(ts.URL, id, "1")); resp.StatusCode != http.StatusOK {
			t.Fatalf("second addStay status = %d, want 200, body:\n%s", resp.StatusCode, body(t, resp))
		}
		page := body(t, httpDo(t, http.MethodGet, ts.URL+"/trips/"+strconv.FormatInt(id, 10)))
		if !strings.Contains(page, fmt.Sprintf(bookMarker, id)) {
			t.Errorf("expected a Book it button for a partly-booked trip, got:\n%s", page)
		}
		if !strings.Contains(page, fmt.Sprintf(unbookMarker, id)) {
			t.Errorf("expected an Unbook button for a partly-booked trip, got:\n%s", page)
		}
	})
}

// TestBookTrip_UnknownTripIs404 and its unbook counterpart prove both
// routes 404 on an unknown trip id, via h.getTripOr404 — matching every
// other per-trip handler's existence check.
func TestBookTrip_UnknownTripIs404(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	resp := httpDo(t, http.MethodPost, bookEndpoint(ts.URL, 999999))
	got := body(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("bookTrip(unknown id) status = %d, want 404, body:\n%s", resp.StatusCode, got)
	}
}

func TestUnbookTrip_UnknownTripIs404(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	resp := httpDo(t, http.MethodPost, unbookEndpoint(ts.URL, 999999))
	got := body(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("unbookTrip(unknown id) status = %d, want 404, body:\n%s", resp.StatusCode, got)
	}
}
