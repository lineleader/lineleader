package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lineleader/lineleader/internal/dvc"
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
		Plans:      nil,
		PlansPath:  filepath.Join(dir, "plans.json"),
		Defaults: Defaults{
			From:      "2026-01-04",
			To:        "2026-01-08",
			Budget:    "100",
			MinNights: "1",
		},
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

func TestIndex_RendersDefaults(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	got := body(t, resp)
	if !strings.Contains(got, `value="100"`) {
		t.Errorf("expected default budget 100 in response, got:\n%s", got)
	}
	if !strings.Contains(got, "Test Resort") {
		t.Errorf("expected fixture resort 'Test Resort' in initial render, got:\n%s", got)
	}
}

func TestUpdateField_ReturnsResults(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	form := url.Values{
		"from":       {"2026-01-04"},
		"to":         {"2026-01-08"},
		"min_nights": {"1"},
	}
	resp, err := http.PostForm(ts.URL+"/trips/0/field", form)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	got := body(t, resp)
	if !strings.Contains(got, `id="trip-0-results"`) {
		t.Errorf("expected results fragment with id, got:\n%s", got)
	}
	if !strings.Contains(got, "Test Resort") {
		t.Errorf("expected 'Test Resort' in results fragment, got:\n%s", got)
	}
}

func TestSelectAndField_ShowsCheckmark(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	// Select row 0 of trip 0. This collapses the trip, so expand it again to
	// inspect the results table.
	if _, err := http.Post(ts.URL+"/trips/0/select/0", "", nil); err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(ts.URL+"/trips/0/collapse", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	got := body(t, resp)
	if !strings.Contains(got, `class="selected"`) {
		t.Errorf("expected selected row class after select, got:\n%s", got)
	}
	if !strings.Contains(got, "✓") {
		t.Errorf("expected ✓ check mark on selected row, got:\n%s", got)
	}

	// Now post a field change and confirm the selection still shows in the fragment.
	form := url.Values{
		"from":       {"2026-01-04"},
		"to":         {"2026-01-08"},
		"min_nights": {"1"},
	}
	resp2, err := http.PostForm(ts.URL+"/trips/0/field", form)
	if err != nil {
		t.Fatal(err)
	}
	got2 := body(t, resp2)
	if !strings.Contains(got2, `class="selected"`) {
		t.Errorf("expected selected row class to persist after field change, got:\n%s", got2)
	}
}

func TestSelect_CollapsesTrip(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	// Selecting a room collapses the trip so the user can move to the next one.
	resp, err := http.Post(ts.URL+"/trips/0/select/0", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	got := body(t, resp)
	if !strings.Contains(got, `class="trip collapsed"`) {
		t.Errorf("expected trip to collapse after selecting a room, got:\n%s", got)
	}

	// Deselecting the same room expands the trip again so it can be re-picked.
	resp2, err := http.Post(ts.URL+"/trips/0/select/0", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	got2 := body(t, resp2)
	if strings.Contains(got2, `class="trip collapsed"`) {
		t.Errorf("expected trip to expand after deselecting a room, got:\n%s", got2)
	}
}

func TestSelect_SetsCollapsedAndRendersApp(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	// Selecting a row goes through the Planner, then the web sets its view-only
	// collapsed flag and re-renders the whole #app partial.
	resp, err := http.Post(ts.URL+"/trips/0/select/0", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	got := body(t, resp)
	// The app partial is rendered (#app root + budget bar present).
	if !strings.Contains(got, `id="app"`) {
		t.Errorf("expected #app root in select response, got:\n%s", got)
	}
	// The selected trip is collapsed (view-only state applied from the snapshot).
	if !strings.Contains(got, `class="trip collapsed"`) {
		t.Errorf("expected selected trip collapsed in app render, got:\n%s", got)
	}
	// The collapsed summary shows the selected room (selection survives render).
	if !strings.Contains(got, "✓") && !strings.Contains(got, "Test Resort") {
		t.Errorf("expected selection reflected in collapsed summary, got:\n%s", got)
	}
}

func TestSavePlanAndLoad_RestoresSelection(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	// Select row 0 of trip 0, then save the plan with that selection.
	http.Post(ts.URL+"/trips/0/select/0", "", nil)
	http.PostForm(ts.URL+"/plans", url.Values{"name": {"summer"}})

	// Clear the selection by toggling the same row off.
	resp, err := http.Post(ts.URL+"/trips/0/select/0", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := body(t, resp); strings.Contains(got, "✓") {
		t.Fatalf("expected selection cleared before load, got:\n%s", got)
	}

	// Loading the plan should restore the saved selection.
	resp2, err := http.Post(ts.URL+"/plans/summer/load", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	got2 := body(t, resp2)
	if !strings.Contains(got2, "✓") {
		t.Errorf("expected selection restored after load, got:\n%s", got2)
	}
}

func TestToggleResortFilter_PersistsAndAffectsResults(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	// Toggle resort TST off — fixture has only one resort, so results should empty.
	resp, err := http.Post(ts.URL+"/filters/resorts/TST", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	got := body(t, resp)
	if !strings.Contains(got, `hx-swap-oob="true"`) {
		t.Errorf("expected OOB swap of trip-list, got:\n%s", got)
	}
	// Trip results table should now be empty (no resort cell).
	if strings.Contains(got, "<td>Test Resort</td>") {
		t.Errorf("after excluding TST, no result rows expected, got:\n%s", got)
	}

	// Hit / again and confirm exclusion sticks.
	resp2, _ := http.Get(ts.URL + "/")
	got2 := body(t, resp2)
	if strings.Contains(got2, "<td>Test Resort</td>") {
		t.Errorf("expected no Test Resort row in results after exclusion, got:\n%s", got2)
	}
}

func TestSavePlanAndLoad(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	// Save a plan.
	resp, err := http.PostForm(ts.URL+"/plans", url.Values{"name": {"summer"}})
	if err != nil {
		t.Fatal(err)
	}
	got := body(t, resp)
	if !strings.Contains(got, "summer") {
		t.Errorf("expected saved plan 'summer' to appear in panel, got:\n%s", got)
	}

	// Load the plan back.
	resp2, err := http.Post(ts.URL+"/plans/summer/load", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	got2 := body(t, resp2)
	if !strings.Contains(got2, "Plan: summer") {
		t.Errorf("expected 'Plan: summer' marker after load, got:\n%s", got2)
	}
}
