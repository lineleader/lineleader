package web

import (
	"context"
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

// twoResortCharts returns a chart set covering two resorts (TST and TS2)
// over the same January 2026 window, so a filter test can exclude one and
// still expect results from the other.
func twoResortCharts() []*dvc.ResortChart {
	season := func(sunThu, friSat int) dvc.Season {
		return dvc.Season{
			Periods: []dvc.DateRange{{Start: "2026-01-01", End: "2026-01-31"}},
			SunThu:  []int{sunThu},
			FriSat:  []int{friSat},
		}
	}
	return []*dvc.ResortChart{
		{
			ResortName: "Test Resort One",
			ResortCode: "TST",
			Year:       2026,
			Columns:    []dvc.Column{{RoomType: "STUDIO", View: "R", Sleeps: 4}},
			Seasons:    []dvc.Season{season(10, 14)},
		},
		{
			ResortName: "Test Resort Two",
			ResortCode: "TS2",
			Year:       2026,
			Columns:    []dvc.Column{{RoomType: "STUDIO", View: "R", Sleeps: 4}},
			Seasons:    []dvc.Season{season(12, 16)},
		},
	}
}

// newLedgerTestServerWithCharts is newLedgerTestServer parameterized over
// the chart set, for tests that need more than one resort.
func newLedgerTestServerWithCharts(t *testing.T, charts []*dvc.ResortChart) (*httptest.Server, *ledger.Store) {
	t.Helper()
	dir := t.TempDir()
	store := ledger.OpenTest(t)
	srv := NewServer(Options{
		Charts:     charts,
		ConfigPath: filepath.Join(dir, "config.json"),
		Ledger:     store,
	})
	return httptest.NewServer(srv), store
}

// addBudgetContract seeds a single unposted contract giving a trip a real,
// non-zero effective budget (Total == AnnualPoints when nothing has yet
// been allotted or used — see ledger.BudgetForUseYear).
func addBudgetContract(t *testing.T, store *ledger.Store, annualPoints int, useYearMonth time.Month) {
	t.Helper()
	if _, err := store.AddContract(context.Background(), ledger.Contract{
		Name:         "C1",
		AnnualPoints: annualPoints,
		UseYearMonth: useYearMonth,
		TermYears:    10,
	}); err != nil {
		t.Fatalf("AddContract: %v", err)
	}
}

// createTripViaForm submits the trip-creation form against base and returns
// the new trip's id, following the 303 redirect's Location header.
func createTripViaForm(t *testing.T, base string, form url.Values) int64 {
	t.Helper()
	client := noRedirectClient()
	resp, err := client.PostForm(base+"/trips", form)
	if err != nil {
		t.Fatalf("create trip: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("create trip status = %d, want 303", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	idStr := strings.TrimPrefix(loc, "/trips/")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		t.Fatalf("parsing trip id from Location %q: %v", loc, err)
	}
	return id
}

// TestTripPage_RendersSearchResults proves dvc.Search reaches the trip page:
// a trip window the minimal test chart covers, with a contract giving a
// real budget, must render at least one result row, and rows must be in
// ascending Points order — the order dvc.Search itself promises.
func TestTripPage_RendersSearchResults(t *testing.T) {
	ts, store := newLedgerTestServer(t)
	defer ts.Close()

	// minimalChart: TST, studio, Jan 2026, 10pts Sun-Thu / 14pts Fri-Sat.
	addBudgetContract(t, store, 100, time.January)

	form := url.Values{
		"name":       {"Search trip"},
		"from":       {"2026-01-05"},
		"to":         {"2026-01-20"},
		"min_nights": {"3"},
	}
	id := createTripViaForm(t, ts.URL, form)

	resp, err := http.Get(ts.URL + "/trips/" + strconv.FormatInt(id, 10))
	if err != nil {
		t.Fatal(err)
	}
	got := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("trip page status = %d, want 200, body:\n%s", resp.StatusCode, got)
	}
	if !strings.Contains(got, "results-table") {
		t.Fatalf("expected a results table in body, got:\n%s", got)
	}

	// Extract each row's PTS column value in document order and assert it's
	// non-decreasing — proof search results reached the page unshuffled.
	points := extractResultPoints(t, got)
	if len(points) == 0 {
		t.Fatalf("expected at least one result row, got none. body:\n%s", got)
	}
	for i := 1; i < len(points); i++ {
		if points[i] < points[i-1] {
			t.Errorf("result rows not ascending by points: %v", points)
			break
		}
	}
}

// extractResultPoints pulls the PTS column value out of each results-table
// row, in document order, by locating each "<td>N</td>" that is the 7th
// <td> within a <tr> — brittle, but this package has no HTML parser
// dependency and the results table's column order is pinned by
// results.html.
func extractResultPoints(t *testing.T, html string) []int {
	t.Helper()
	var points []int
	rows := strings.Split(html, "<tr")
	for _, row := range rows {
		if !strings.Contains(row, "select-btn") {
			continue // not a results row (header row or unrelated <tr>)
		}
		tds := strings.Split(row, "<td>")
		// tds[0] is pre-first-<td> junk (the select button cell uses <td>
		// too, so column indices below count from tds[1]).
		// Columns in results.html: [0]=select btn wrapper (no plain <td>
		// text), RESORT, ROOM TYPE, VIEW, CHECK-IN, CHECK-OUT, NIGHTS, PTS.
		if len(tds) < 8 {
			continue
		}
		ptsCell := tds[7]
		end := strings.Index(ptsCell, "<")
		if end < 0 {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(ptsCell[:end]))
		if err != nil {
			continue
		}
		points = append(points, n)
	}
	return points
}

// TestTripPage_SearchRespectsGlobalFilters excludes one of two resorts via
// the global filter handler, then loads the trip page: the excluded
// resort's name must not appear in the results table.
func TestTripPage_SearchRespectsGlobalFilters(t *testing.T) {
	ts, store := newLedgerTestServerWithCharts(t, twoResortCharts())
	defer ts.Close()

	addBudgetContract(t, store, 200, time.January)

	// Exclude Test Resort Two (TS2) globally.
	resp, err := http.Post(ts.URL+"/filters/resorts/TS2", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	form := url.Values{
		"name":       {"Filtered trip"},
		"from":       {"2026-01-05"},
		"to":         {"2026-01-20"},
		"min_nights": {"3"},
	}
	id := createTripViaForm(t, ts.URL, form)

	resp2, err := http.Get(ts.URL + "/trips/" + strconv.FormatInt(id, 10))
	if err != nil {
		t.Fatal(err)
	}
	got := body(t, resp2)
	if !strings.Contains(got, "Test Resort One") {
		t.Errorf("expected Test Resort One (not excluded) in results, got:\n%s", got)
	}
	if strings.Contains(got, "Test Resort Two") {
		t.Errorf("expected Test Resort Two (excluded) absent from results, got:\n%s", got)
	}
}

// TestTripPage_SearchRespectsTripFilterOverride proves searchTrip resolves
// a trip's OWN exclusions when FilterMode is override, rather than always
// searching with the global filter set — the case
// TestTripPage_SearchRespectsGlobalFilters cannot catch on its own, since
// an inherit-mode trip's effective filters equal the global set by
// definition. Here the global filters exclude nothing, but the trip
// overrides with its own exclusion of TS2: TS2 must still be absent.
func TestTripPage_SearchRespectsTripFilterOverride(t *testing.T) {
	ts, store := newLedgerTestServerWithCharts(t, twoResortCharts())
	defer ts.Close()
	ctx := context.Background()

	addBudgetContract(t, store, 200, time.January)

	id, err := store.AddTrip(ctx, ledger.Trip{
		Name:             "Override trip",
		StartDate:        dateParse(t, "2026-01-05"),
		EndDate:          dateParse(t, "2026-01-20"),
		MinNights:        3,
		FilterMode:       ledger.TripFilterOverride,
		ExcludeResorts:   []string{"TS2"},
		ExcludeRoomTypes: nil,
	})
	if err != nil {
		t.Fatalf("AddTrip: %v", err)
	}

	resp, err := http.Get(ts.URL + "/trips/" + strconv.FormatInt(id, 10))
	if err != nil {
		t.Fatal(err)
	}
	got := body(t, resp)
	if !strings.Contains(got, "Test Resort One") {
		t.Errorf("expected Test Resort One (not excluded by the trip override) in results, got:\n%s", got)
	}
	if strings.Contains(got, "Test Resort Two") {
		t.Errorf("expected Test Resort Two (excluded by the trip's own FilterMode=override) absent from results, got:\n%s", got)
	}
}

// TestUpdateTrip_PersistsAndReSearches proves POST /trips/{id} updates the
// stored trip and re-runs the search over the new window.
func TestUpdateTrip_PersistsAndReSearches(t *testing.T) {
	ts, store := newLedgerTestServer(t)
	defer ts.Close()
	ctx := context.Background()

	id := createTripViaForm(t, ts.URL, url.Values{
		"name":       {"Original name"},
		"from":       {"2026-01-05"},
		"to":         {"2026-01-10"},
		"min_nights": {"2"},
	})

	form := url.Values{
		"name":       {"Renamed trip"},
		"from":       {"2026-01-01"},
		"to":         {"2026-01-31"},
		"min_nights": {"3"},
	}
	resp, err := http.PostForm(ts.URL+"/trips/"+strconv.FormatInt(id, 10), form)
	if err != nil {
		t.Fatal(err)
	}
	got := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update status = %d, want 200, body:\n%s", resp.StatusCode, got)
	}
	if !strings.Contains(got, "Renamed trip") {
		t.Errorf("expected new name in body, got:\n%s", got)
	}

	updated, err := store.GetTrip(ctx, id)
	if err != nil {
		t.Fatalf("GetTrip: %v", err)
	}
	if updated.Name != "Renamed trip" {
		t.Errorf("stored Name = %q, want %q", updated.Name, "Renamed trip")
	}
	if updated.MinNights != 3 {
		t.Errorf("stored MinNights = %d, want 3", updated.MinNights)
	}
	wantStart := dateParse(t, "2026-01-01")
	wantEnd := dateParse(t, "2026-01-31")
	if !updated.StartDate.Equal(wantStart) || !updated.EndDate.Equal(wantEnd) {
		t.Errorf("stored window = %s..%s, want %s..%s", updated.StartDate, updated.EndDate, wantStart, wantEnd)
	}
}

// TestUpdateTrip_PreservesFiltersAndOverride is the trap test: parseTripForm
// only fills Name/StartDate/EndDate/MinNights, so a handler that passes its
// return value straight to UpdateTrip silently wipes the trip's FilterMode,
// ExcludeResorts, ExcludeRoomTypes and BudgetOverride. Seed all four
// non-default, change only the name, and assert all four survive.
func TestUpdateTrip_PreservesFiltersAndOverride(t *testing.T) {
	ts, store := newLedgerTestServer(t)
	defer ts.Close()
	ctx := context.Background()

	override := 123
	id, err := store.AddTrip(ctx, ledger.Trip{
		Name:             "Has overrides",
		StartDate:        dateParse(t, "2026-01-05"),
		EndDate:          dateParse(t, "2026-01-10"),
		MinNights:        2,
		FilterMode:       ledger.TripFilterOverride,
		ExcludeResorts:   []string{"TST"},
		ExcludeRoomTypes: []string{"STUDIO"},
		BudgetOverride:   &override,
	})
	if err != nil {
		t.Fatalf("AddTrip: %v", err)
	}

	form := url.Values{
		"name":       {"Still has overrides"},
		"from":       {"2026-01-05"},
		"to":         {"2026-01-10"},
		"min_nights": {"2"},
	}
	resp, err := http.PostForm(ts.URL+"/trips/"+strconv.FormatInt(id, 10), form)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update status = %d, want 200", resp.StatusCode)
	}

	got, err := store.GetTrip(ctx, id)
	if err != nil {
		t.Fatalf("GetTrip: %v", err)
	}
	if got.Name != "Still has overrides" {
		t.Errorf("Name = %q, want the updated name", got.Name)
	}
	if got.FilterMode != ledger.TripFilterOverride {
		t.Errorf("FilterMode = %q, want %q (preserved)", got.FilterMode, ledger.TripFilterOverride)
	}
	if len(got.ExcludeResorts) != 1 || got.ExcludeResorts[0] != "TST" {
		t.Errorf("ExcludeResorts = %v, want [TST] (preserved)", got.ExcludeResorts)
	}
	if len(got.ExcludeRoomTypes) != 1 || got.ExcludeRoomTypes[0] != "STUDIO" {
		t.Errorf("ExcludeRoomTypes = %v, want [STUDIO] (preserved)", got.ExcludeRoomTypes)
	}
	if got.BudgetOverride == nil || *got.BudgetOverride != 123 {
		t.Errorf("BudgetOverride = %v, want *123 (preserved)", got.BudgetOverride)
	}
}

// TestUpdateTrip_ValidationError proves a rejected update renders 200 (not
// 400) with the error text, following the ledger handlers' inline-error
// convention, and leaves the stored trip untouched.
func TestUpdateTrip_ValidationError(t *testing.T) {
	ts, store := newLedgerTestServer(t)
	defer ts.Close()
	ctx := context.Background()

	id, err := store.AddTrip(ctx, ledger.Trip{
		Name:      "Unchanged",
		StartDate: dateParse(t, "2026-01-05"),
		EndDate:   dateParse(t, "2026-01-10"),
		MinNights: 2,
	})
	if err != nil {
		t.Fatalf("AddTrip: %v", err)
	}

	form := url.Values{
		"name":       {"Should not stick"},
		"from":       {"2026-01-05"},
		"to":         {"2026-01-10"},
		"min_nights": {"31"},
	}
	resp, err := http.PostForm(ts.URL+"/trips/"+strconv.FormatInt(id, 10), form)
	if err != nil {
		t.Fatal(err)
	}
	got := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200, body:\n%s", resp.StatusCode, got)
	}
	if !strings.Contains(got, "30-night limit") {
		t.Errorf("expected 30-night limit error text in body, got:\n%s", got)
	}

	stored, err := store.GetTrip(ctx, id)
	if err != nil {
		t.Fatalf("GetTrip: %v", err)
	}
	if stored.Name != "Unchanged" {
		t.Errorf("Name = %q, want %q (unchanged)", stored.Name, "Unchanged")
	}
	if stored.MinNights != 2 {
		t.Errorf("MinNights = %d, want 2 (unchanged)", stored.MinNights)
	}
}

// TestTripPage_SpanWarningRendered proves the span-use-years banner reaches
// the rendered page end-to-end: a trip window straddling an April use-year
// boundary, with a matching contract, must render .span-warning text.
func TestTripPage_SpanWarningRendered(t *testing.T) {
	ts, store := newLedgerTestServer(t)
	defer ts.Close()

	addBudgetContract(t, store, 100, time.April)

	id := createTripViaForm(t, ts.URL, url.Values{
		"name":       {"Spanning trip"},
		"from":       {"2026-03-20"},
		"to":         {"2026-04-10"},
		"min_nights": {"3"},
	})

	resp, err := http.Get(ts.URL + "/trips/" + strconv.FormatInt(id, 10))
	if err != nil {
		t.Fatal(err)
	}
	got := body(t, resp)
	if !strings.Contains(got, "span-warning") {
		t.Errorf("expected .span-warning element in body, got:\n%s", got)
	}
	if !strings.Contains(got, "use years 2025 → 2026") {
		t.Errorf("expected span note text in body, got:\n%s", got)
	}
}
