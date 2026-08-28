package web

import (
	"context"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lineleader/lineleader/internal/dvc"
	"github.com/lineleader/lineleader/internal/ledger"
)

func minimalChart() *dvc.ResortChart {
	return &dvc.ResortChart{
		ResortName: "Test Resort",
		ResortCode: "TST",
		Year:       2026,
		Columns: []dvc.Column{
			{RoomType: "STUDIO", View: "R", Sleeps: 4},
		},
		Seasons: []dvc.Season{
			{
				Periods: []dvc.DateRange{{Start: "2026-01-01", End: "2026-01-31"}},
				SunThu:  []int{10},
				FriSat:  []int{14},
			},
		},
	}
}

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	dir := t.TempDir()
	srv := NewServer(Options{
		Charts:     []*dvc.ResortChart{minimalChart()},
		Config:     dvc.Config{},
		ConfigPath: filepath.Join(dir, "config.json"),
		Ledger:     ledger.OpenTest(t),
	})
	return httptest.NewServer(srv)
}

func body(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

func TestNewServer_PanicsWithoutLedger(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected NewServer to panic without Options.Ledger")
		}
	}()
	NewServer(Options{})
}

func TestTripList_EmptyState(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	got := body(t, resp)
	if !strings.Contains(got, "No trips yet") {
		t.Errorf("expected empty state message, got:\n%s", got)
	}
}

func TestCreateTrip_PersistsAndRedirects(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	form := url.Values{
		"name":       {"Test Trip"},
		"from":       {"2026-06-04"},
		"to":         {"2026-06-10"},
		"min_nights": {"3"},
	}
	client := noRedirectClient()
	resp, err := client.PostForm(ts.URL+"/trips", form)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if !strings.HasPrefix(loc, "/trips/") {
		t.Fatalf("Location = %q, want prefix /trips/", loc)
	}

	resp2, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	got := body(t, resp2)
	if !strings.Contains(got, "Test Trip") {
		t.Errorf("expected trip name in list, got:\n%s", got)
	}
}

// TestCreateTrip_ValidationErrors is table-driven over every rejected
// submission: each must render 200 with the error text in the body and
// create NO trip — the assertion that actually bites is the "no trip
// created" one (a handler that redirects before validating would still
// pass a body-text-only check).
func TestCreateTrip_ValidationErrors(t *testing.T) {
	ts, store := newLedgerTestServer(t)
	defer ts.Close()

	base := func() url.Values {
		return url.Values{
			"name":       {"Test Trip"},
			"from":       {"2026-06-04"},
			"to":         {"2026-06-10"},
			"min_nights": {"3"},
		}
	}

	cases := []struct {
		name    string
		mutate  func(url.Values)
		wantErr string
	}{
		{"empty name", func(v url.Values) { v.Set("name", "") }, "name is required"},
		{"unparseable date", func(v url.Values) { v.Set("from", "not-a-date") }, "invalid date"},
		{"start after end", func(v url.Values) { v.Set("from", "2026-06-10"); v.Set("to", "2026-06-04") }, "start date must be before end date"},
		{"min nights zero", func(v url.Values) { v.Set("min_nights", "0") }, "min nights must be at least 1"},
		// "Disney's" is HTML-escaped to "Disney&#39;s" in the rendered page,
		// so match around the apostrophe rather than the literal text.
		{"min nights over limit", func(v url.Values) { v.Set("min_nights", "31") }, "30-night limit"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			form := base()
			c.mutate(form)

			resp, err := http.PostForm(ts.URL+"/trips", form)
			if err != nil {
				t.Fatal(err)
			}
			got := body(t, resp)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200, body:\n%s", resp.StatusCode, got)
			}
			if !strings.Contains(got, c.wantErr) {
				t.Errorf("expected error text %q in body, got:\n%s", c.wantErr, got)
			}

			trips, err := store.ListTrips(context.Background())
			if err != nil {
				t.Fatalf("ListTrips: %v", err)
			}
			if len(trips) != 0 {
				t.Fatalf("expected no trip created for case %q, got %d trips", c.name, len(trips))
			}
		})
	}
}

// TestTripPage_RendersBudgetBreakdown seeds two contracts (120 + 150 annual
// points, April use year) and an allotment/usage history matching golden
// example A from the budget design doc (see budget_test.go's "healthy"
// case): UseYear 2025 net +70 (banked forward), UseYear 2026 net +270
// (current), no UseYear 2027 activity (fully borrowable) — Total 610. A
// trip starting 2026-06-04 falls in use year 2026, so the trip page must
// show use year 2026 and total 610: end-to-end proof the ledger budget
// reaches the page.
func TestTripPage_RendersBudgetBreakdown(t *testing.T) {
	ts, store := newLedgerTestServer(t)
	defer ts.Close()
	ctx := context.Background()

	if _, err := store.AddContract(ctx, ledger.Contract{
		Name: "C1", AnnualPoints: 120, UseYearMonth: time.April, TermYears: 10, PurchasePrice: 50_000_00,
	}); err != nil {
		t.Fatalf("AddContract 1: %v", err)
	}
	if _, err := store.AddContract(ctx, ledger.Contract{
		Name: "C2", AnnualPoints: 150, UseYearMonth: time.April, TermYears: 10, PurchasePrice: 60_000_00,
	}); err != nil {
		t.Fatalf("AddContract 2: %v", err)
	}

	addEntry := func(useYear int, kind string, allotted, used int, date string) {
		t.Helper()
		if _, err := store.AddEntry(ctx, ledger.Entry{
			UseYear: useYear, Date: dateParse(t, date), Desc: "seed", Kind: kind, Allotted: allotted, Used: used,
		}); err != nil {
			t.Fatalf("AddEntry: %v", err)
		}
	}
	// UseYear 2025: allotted 270, used 200 -> Net 70 (banked forward).
	addEntry(2025, ledger.KindAllocation, 270, 0, "2025-04-01")
	addEntry(2025, ledger.KindUsage, 0, 200, "2025-05-01")
	// UseYear 2026: allotted 270, used 0 -> Net 270 (current).
	addEntry(2026, ledger.KindAllocation, 270, 0, "2026-04-01")

	form := url.Values{
		"name":       {"Summer trip"},
		"from":       {"2026-06-04"},
		"to":         {"2026-06-10"},
		"min_nights": {"3"},
	}
	resp, err := http.PostForm(ts.URL+"/trips", form)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("trip page status = %d, want 200 (client follows the 303)", resp.StatusCode)
	}
	got := body(t, resp)
	// Match the rendered heading, not a bare "2026" — the trip's own dates
	// (2026-06-04) put that substring on the page regardless of the budget,
	// so a bare Contains would pass even with the use year zeroed out.
	if !strings.Contains(got, "use year 2026") {
		t.Errorf("expected use year 2026 in body, got:\n%s", got)
	}
	// Golden example A: current +270, banked +70, borrowable +270, total 610.
	// Pin each ROW, not bare numbers: the components must be individually
	// right, so a wrong decomposition that still sums to 610 fails too.
	// html/template escapes the leading "+" of a signed label to "&#43;".
	for _, want := range []string{
		"<dt>Current</dt><dd>&#43;270</dd>",
		"<dt>Banked</dt><dd>&#43;70</dd>",
		"<dt>Borrowable</dt><dd>&#43;270</dd>",
		"<dt>Total</dt><dd>610</dd>",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in budget breakdown, got:\n%s", want, got)
		}
	}
}

func TestTripPage_NotFound(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/trips/999")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestDeleteTrip_RemovesAndRedirects(t *testing.T) {
	ts, store := newLedgerTestServer(t)
	defer ts.Close()
	ctx := context.Background()

	id, err := store.AddTrip(ctx, ledger.Trip{
		Name:      "To delete",
		StartDate: dateParse(t, "2026-06-04"),
		EndDate:   dateParse(t, "2026-06-10"),
		MinNights: 3,
	})
	if err != nil {
		t.Fatalf("AddTrip: %v", err)
	}

	client := noRedirectClient()
	req, err := http.NewRequest(http.MethodDelete, ts.URL+fmt.Sprintf("/trips/%d", id), nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/" {
		t.Fatalf("Location = %q, want /", loc)
	}

	trips, err := store.ListTrips(ctx)
	if err != nil {
		t.Fatalf("ListTrips: %v", err)
	}
	for _, tr := range trips {
		if tr.ID == id {
			t.Fatalf("trip %d still present after delete", id)
		}
	}
}

// TestToggleResortFilter_PersistsAndAffectsResults keeps the persistence
// half of its planner-era namesake: there are no results on the home page
// anymore (search is ixe.10), so only the OOB swap + config.json persistence
// are asserted now.
func TestToggleResortFilter_PersistsAndAffectsResults(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/filters/resorts/TST", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	got := body(t, resp)
	if !strings.Contains(got, `hx-swap-oob="true"`) {
		t.Errorf("expected OOB swap of trip-list, got:\n%s", got)
	}

	// Confirm the exclusion persisted: reopening the filter panel shows TST
	// (Test Resort) excluded.
	resp2, err := http.Get(ts.URL + "/filters")
	if err != nil {
		t.Fatal(err)
	}
	panel := body(t, resp2)
	if !strings.Contains(panel, "[ ] Test Resort") {
		t.Errorf("expected Test Resort excluded after toggle, got:\n%s", panel)
	}
}

// TestGlobalFilterPanel_NoRegression verifies the global panel still renders the
// global URLs and global title — unchanged by the scope parameterization.
func TestGlobalFilterPanel_NoRegression(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/filters")
	if err != nil {
		t.Fatal(err)
	}
	got := body(t, resp)

	if !strings.Contains(got, `hx-post="/filters/resorts/TST"`) {
		t.Errorf("expected global resort URL unchanged, got:\n%s", got)
	}
	if !strings.Contains(got, "Filters — Global") {
		t.Errorf("expected global title, got:\n%s", got)
	}
	// Global panel must not include the per-trip mode switch.
	if strings.Contains(got, `/filters/mode`) {
		t.Errorf("global panel should not have mode switch, got:\n%s", got)
	}
}

// TestLedgerDuesMutationInvalidatesCostProvider is the carried-over test for
// the ledger_handlers.go Invalidate() calls: a dues-rate edit made through
// the ledger handler must be reflected the next time the planner's
// costProvider is asked for a CostBasis, rather than serving the value it
// cached up to costBasisTTL ago. It exercises costProvider and
// ledgerHandlers directly (not through NewServer) so it can inspect
// costs.Basis() itself.
func TestLedgerDuesMutationInvalidatesCostProvider(t *testing.T) {
	store := ledger.OpenTest(t)

	// A priced contract, so CostBasis.Known() is true — seed.sql already
	// stores dues rates back to 2019, including 2026.
	if _, err := store.AddContract(context.Background(), ledger.Contract{
		Name:          "C1",
		AnnualPoints:  100,
		UseYearMonth:  time.January,
		TermYears:     10,
		PurchasePrice: 100_000_00, // $100,000.00; the exact rate isn't asserted here, only that dues change
	}); err != nil {
		t.Fatalf("AddContract: %v", err)
	}

	costs := newCostProvider(store)
	basis1, ok := costs.Basis(context.Background())
	if !ok || !basis1.Known() {
		t.Fatalf("Basis() = (%+v, %v), want a known basis", basis1, ok)
	}
	rate1, _ := basis1.DuesFor(2026)
	if rate1 != 8_223_500 {
		t.Fatalf("seeded 2026 dues rate = %v, want 8_223_500 ($8.2235, per seed.sql)", rate1)
	}

	tmpl := template.Must(template.New("").Funcs(templateFuncs()).ParseFS(templatesFS, "templates/*.html"))
	lh := &ledgerHandlers{tmpl: tmpl, store: store, costs: costs}

	// Prime the cache again so the mutation below has something stale to
	// invalidate (Basis() above already cached it, but be explicit).
	costs.Basis(context.Background())

	form := url.Values{"year": {"2026"}, "rate": {"20"}}
	req := httptest.NewRequest(http.MethodPost, "/ledger/dues?view=contracts", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	lh.upsertDues(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("upsertDues status = %d, body = %s", w.Code, w.Body.String())
	}

	basis2, ok := costs.Basis(context.Background())
	if !ok {
		t.Fatalf("Basis() after mutation: ok = false, want true")
	}
	rate2, _ := basis2.DuesFor(2026)
	if rate2 != 20_000_000 {
		t.Errorf("2026 dues rate after mutation = %v, want 20_000_000 ($20.00) — Invalidate() was not called, or Basis() served a stale cache", rate2)
	}
}

// TestDeleteTrip_HxRedirect pins the htmx path. The delete button on the trip
// list is an hx-delete with no hx-target, so a bare 303 would be followed by
// htmx's own fetch and the whole trips page swapped into the button's <td>.
// Answer an htmx request with HX-Redirect instead, matching the pattern
// authMiddleware already uses for /login.
func TestDeleteTrip_HxRedirect(t *testing.T) {
	ts, store := newLedgerTestServer(t)
	defer ts.Close()
	ctx := context.Background()

	id, err := store.AddTrip(ctx, ledger.Trip{
		Name:      "To delete",
		StartDate: dateParse(t, "2026-06-04"),
		EndDate:   dateParse(t, "2026-06-10"),
		MinNights: 3,
	})
	if err != nil {
		t.Fatalf("AddTrip: %v", err)
	}

	client := noRedirectClient()
	req, err := http.NewRequest(http.MethodDelete, ts.URL+fmt.Sprintf("/trips/%d", id), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("HX-Request", "true")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if got := resp.Header.Get("HX-Redirect"); got != "/" {
		t.Errorf("HX-Redirect = %q, want /", got)
	}
	// Must NOT be a 3xx: htmx would follow it and swap the redirected page
	// into the target, which is the bug this test exists to prevent.
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		t.Errorf("status = %d, want a non-redirect status for an htmx request", resp.StatusCode)
	}

	trips, err := store.ListTrips(ctx)
	if err != nil {
		t.Fatalf("ListTrips: %v", err)
	}
	for _, tr := range trips {
		if tr.ID == id {
			t.Fatalf("trip %d still present after delete", id)
		}
	}
}
