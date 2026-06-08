package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
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

// roomTypeChart is a fixture whose room type contains a space, used to verify
// per-trip route URL-decoding of {name}.
func roomTypeChart() *dvc.ResortChart {
	return &dvc.ResortChart{
		ResortName: "Villa Resort",
		ResortCode: "VLA",
		Year:       2026,
		Columns: []dvc.Column{
			{RoomType: "ONE-BEDROOM VILLA", View: "R", Sleeps: 4},
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

// twoResortCharts returns two distinct resorts so seeding/isolation tests can
// exclude one resort globally and a DIFFERENT one per trip, then assert both
// exclusions hold.
func twoResortCharts() []*dvc.ResortChart {
	mk := func(name, code string) *dvc.ResortChart {
		return &dvc.ResortChart{
			ResortName: name,
			ResortCode: code,
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
	return []*dvc.ResortChart{mk("Alpha Resort", "ALP"), mk("Beta Resort", "BTA")}
}

func newTestServerWithCharts(t *testing.T, charts []*dvc.ResortChart) *httptest.Server {
	t.Helper()
	dir := t.TempDir()
	srv := NewServer(Options{
		Charts:     charts,
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

func TestPerTripRoutes_BadIndex_400(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	client := ts.Client()
	cases := []struct {
		method string
		path   string
		body   io.Reader
		ctype  string
	}{
		{"GET", "/trips/x/filters", nil, ""},
		{"GET", "/trips/9/filters", nil, ""},
		{"POST", "/trips/x/filters/mode", strings.NewReader("mode=override"), "application/x-www-form-urlencoded"},
		{"POST", "/trips/9/filters/mode", strings.NewReader("mode=override"), "application/x-www-form-urlencoded"},
		{"POST", "/trips/x/filters/resorts/TST", nil, ""},
		{"POST", "/trips/9/filters/resorts/TST", nil, ""},
		{"POST", "/trips/x/filters/roomtypes/STUDIO", nil, ""},
		{"POST", "/trips/9/filters/roomtypes/STUDIO", nil, ""},
		{"DELETE", "/trips/x/filters", nil, ""},
		{"DELETE", "/trips/9/filters", nil, ""},
	}
	for _, c := range cases {
		req, err := http.NewRequest(c.method, ts.URL+c.path, c.body)
		if err != nil {
			t.Fatal(err)
		}
		if c.ctype != "" {
			req.Header.Set("Content-Type", c.ctype)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s %s: status = %d, want 400", c.method, c.path, resp.StatusCode)
		}
	}
}

func TestOpenTripFilters_RendersPanel(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/trips/0/filters")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	got := body(t, resp)
	if !strings.Contains(got, `id="panel"`) {
		t.Errorf("expected filters panel, got:\n%s", got)
	}
	// Plain open must NOT include an OOB results swap.
	if strings.Contains(got, `hx-swap-oob`) {
		t.Errorf("plain open should not include OOB swap, got:\n%s", got)
	}
}

func TestSetTripFilterMode_UnknownTreatedAsInherit(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	resp, err := http.PostForm(ts.URL+"/trips/0/filters/mode", url.Values{"mode": {"bogus"}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	got := body(t, resp)
	// Unknown mode -> inherit: the trip stays an inherit trip, so no override
	// marker. We verify by querying the panel and checking the mode scope.
	if strings.Contains(got, `data-mode="override"`) {
		t.Errorf("unknown mode should resolve to inherit, got:\n%s", got)
	}
	if !strings.Contains(got, `data-mode="inherit"`) {
		t.Errorf("expected inherit mode marker, got:\n%s", got)
	}
}

// TestSetTripFilterMode_OverrideSeedsThenInheritReverts verifies the mode switch
// round-trip: mode=override seeds the trip's rows from the global checks (a
// globally-excluded resort shows excluded), and mode=inherit clears the override
// so the rows revert to the global view.
func TestSetTripFilterMode_OverrideSeedsThenInheritReverts(t *testing.T) {
	ts := newTestServerWithCharts(t, twoResortCharts())
	defer ts.Close()

	// Globally exclude ALP (BTA stays enabled).
	if _, err := http.Post(ts.URL+"/filters/resorts/ALP", "", nil); err != nil {
		t.Fatal(err)
	}

	// Switch trip 0 to override: rows must reflect global checks (ALP excluded).
	resp, err := http.PostForm(ts.URL+"/trips/0/filters/mode", url.Values{"mode": {"override"}})
	if err != nil {
		t.Fatal(err)
	}
	over := body(t, resp)
	if !strings.Contains(over, `data-mode="override"`) {
		t.Fatalf("expected override mode, got:\n%s", over)
	}
	assertResortExcluded(t, over, "Alpha Resort")
	if !strings.Contains(over, "[✓] Beta Resort") {
		t.Errorf("expected Beta Resort still checked after override seed, got:\n%s", over)
	}

	// Now exclude BTA per-trip so override diverges from global.
	if _, err := http.Post(ts.URL+"/trips/0/filters/resorts/BTA", "", nil); err != nil {
		t.Fatal(err)
	}

	// Switch back to inherit: the override clears and rows revert to the global
	// view (ALP excluded, BTA back to enabled).
	resp2, err := http.PostForm(ts.URL+"/trips/0/filters/mode", url.Values{"mode": {"inherit"}})
	if err != nil {
		t.Fatal(err)
	}
	inh := body(t, resp2)
	if !strings.Contains(inh, `data-mode="inherit"`) {
		t.Fatalf("expected inherit mode after revert, got:\n%s", inh)
	}

	// Re-open the panel to confirm reverted rows mirror global (BTA enabled again).
	resp3, err := http.Get(ts.URL + "/trips/0/filters")
	if err != nil {
		t.Fatal(err)
	}
	panel := body(t, resp3)
	assertResortExcluded(t, panel, "Alpha Resort")
	if !strings.Contains(panel, "[✓] Beta Resort") {
		t.Errorf("expected Beta Resort re-enabled after revert to inherit, got:\n%s", panel)
	}
}

func TestToggleTripResort_SeedsOverrideAndScopesOOB(t *testing.T) {
	// Two trips so we can assert isolation.
	ts := newTestServerWithCharts(t, []*dvc.ResortChart{minimalChart()})
	defer ts.Close()
	http.Post(ts.URL+"/trips", "", nil) // add a second trip

	// Toggle resort TST off on trip 0 (currently inherit). This seeds override.
	resp, err := http.Post(ts.URL+"/trips/0/filters/resorts/TST", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	got := body(t, resp)

	// Panel header reflects override now (seeding flipped the mode).
	if !strings.Contains(got, `data-mode="override"`) {
		t.Errorf("expected override mode after toggling on inherit trip, got:\n%s", got)
	}
	// The affected trip's results are OOB-swapped, targeting trip-0-results.
	if !strings.Contains(got, `id="trip-0-results"`) {
		t.Errorf("expected OOB swap of trip-0-results, got:\n%s", got)
	}
	if !strings.Contains(got, `hx-swap-oob`) {
		t.Errorf("expected hx-swap-oob attribute, got:\n%s", got)
	}
	// Affected trip's results changed: TST excluded -> no result row.
	if strings.Contains(got, "<td>Test Resort</td>") {
		t.Errorf("expected trip-0 results empty after excluding TST, got:\n%s", got)
	}
	// Isolation: the OTHER trip's results must NOT be in the response.
	if strings.Contains(got, `id="trip-1-results"`) {
		t.Errorf("expected only trip-0-results OOB-swapped, not trip-1, got:\n%s", got)
	}
}

// TestToggleTripResort_SeedsOverrideFromGlobal is the key seeding test: a resort
// excluded GLOBALLY must remain excluded in a trip's override set after the first
// per-trip toggle of a DIFFERENT resort. This proves the override was seeded from
// the global filters, not started empty.
func TestToggleTripResort_SeedsOverrideFromGlobal(t *testing.T) {
	ts := newTestServerWithCharts(t, twoResortCharts())
	defer ts.Close()

	// Globally exclude ALP. Trip 0 inherits this, so ALP is excluded there too.
	if _, err := http.Post(ts.URL+"/filters/resorts/ALP", "", nil); err != nil {
		t.Fatal(err)
	}

	// First per-trip toggle on inherit trip 0: exclude BTA. This seeds the
	// override from the global set (which already excludes ALP), then adds BTA.
	resp, err := http.Post(ts.URL+"/trips/0/filters/resorts/BTA", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if got := body(t, resp); !strings.Contains(got, `data-mode="override"`) {
		t.Fatalf("expected trip to become override after toggle, got:\n%s", got)
	}

	// Open the trip's override panel and assert BOTH resorts are excluded. If the
	// override had started empty (no seeding), ALP would show enabled ([✓]) again.
	resp2, err := http.Get(ts.URL + "/trips/0/filters")
	if err != nil {
		t.Fatal(err)
	}
	panel := body(t, resp2)
	if !strings.Contains(panel, `data-mode="override"`) {
		t.Fatalf("expected override panel, got:\n%s", panel)
	}
	assertResortExcluded(t, panel, "Alpha Resort")
	assertResortExcluded(t, panel, "Beta Resort")
}

// assertResortExcluded checks the rendered panel shows the named resort as
// excluded: an unchecked "[ ]" marker on that resort's button.
func assertResortExcluded(t *testing.T, panel, resortName string) {
	t.Helper()
	if !strings.Contains(panel, "[ ] "+resortName) {
		t.Errorf("expected %q to be excluded ([ ]) in panel, got:\n%s", resortName, panel)
	}
	if strings.Contains(panel, "[✓] "+resortName) {
		t.Errorf("expected %q NOT to be checked ([✓]) in panel, got:\n%s", resortName, panel)
	}
}

// tripResultsBlock extracts the HTML of the #trip-{i}-results div from a full
// app render so per-trip assertions don't bleed across trips.
func tripResultsBlock(t *testing.T, html string, i int) string {
	t.Helper()
	marker := `id="trip-` + strconv.Itoa(i) + `-results"`
	start := strings.Index(html, marker)
	if start == -1 {
		t.Fatalf("trip-%d-results block not found in:\n%s", i, html)
	}
	rest := html[start:]
	// Stop at the next trip's results block if present, else end of doc.
	if next := strings.Index(rest[len(marker):], `id="trip-`); next != -1 {
		return rest[:len(marker)+next]
	}
	return rest
}

// TestToggleTripResort_IsolatesOtherTrips verifies that excluding a resort on
// trip 0 (override) leaves trip 1's results unchanged, observed through the full
// app render at GET /.
func TestToggleTripResort_IsolatesOtherTrips(t *testing.T) {
	ts := newTestServerWithCharts(t, twoResortCharts())
	defer ts.Close()
	http.Post(ts.URL+"/trips", "", nil) // add a second trip (index 1)

	// Exclude ALP on trip 0 only (seeds override on trip 0).
	if _, err := http.Post(ts.URL+"/trips/0/filters/resorts/ALP", "", nil); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	html := body(t, resp)

	trip0 := tripResultsBlock(t, html, 0)
	trip1 := tripResultsBlock(t, html, 1)

	// Trip 0: Alpha excluded -> no Alpha row, Beta still present.
	if strings.Contains(trip0, "<td>Alpha Resort</td>") {
		t.Errorf("expected Alpha Resort excluded from trip 0, got:\n%s", trip0)
	}
	if !strings.Contains(trip0, "<td>Beta Resort</td>") {
		t.Errorf("expected Beta Resort still in trip 0, got:\n%s", trip0)
	}
	// Trip 1: inherits global (no exclusions) -> BOTH resorts present, unchanged.
	if !strings.Contains(trip1, "<td>Alpha Resort</td>") {
		t.Errorf("expected Alpha Resort still in trip 1 (isolation), got:\n%s", trip1)
	}
	if !strings.Contains(trip1, "<td>Beta Resort</td>") {
		t.Errorf("expected Beta Resort still in trip 1, got:\n%s", trip1)
	}
}

func TestToggleTripRoomType_URLDecodesSpace(t *testing.T) {
	ts := newTestServerWithCharts(t, []*dvc.ResortChart{roomTypeChart()})
	defer ts.Close()

	// Match how the global room-type route is exercised: build the path with
	// url.PathEscape so the space is encoded, the mux decodes it back.
	name := url.PathEscape("ONE-BEDROOM VILLA")
	resp, err := http.Post(ts.URL+"/trips/0/filters/roomtypes/"+name, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	got := body(t, resp)
	if !strings.Contains(got, `data-mode="override"`) {
		t.Errorf("expected override after toggling room type, got:\n%s", got)
	}
	// The room type was excluded -> no result row for the villa.
	if strings.Contains(got, "<td>Villa Resort</td>") {
		t.Errorf("expected villa results empty after excluding room type, got:\n%s", got)
	}
}

func TestResetTripFilters_BackToInherit(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	// Seed override by toggling a resort off, then reset.
	http.Post(ts.URL+"/trips/0/filters/resorts/TST", "", nil)

	req, _ := http.NewRequest("DELETE", ts.URL+"/trips/0/filters", nil)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	got := body(t, resp)
	if !strings.Contains(got, `data-mode="inherit"`) {
		t.Errorf("expected inherit mode after reset, got:\n%s", got)
	}
	// Back to inherit -> global has no exclusions -> Test Resort row is back.
	if !strings.Contains(got, "<td>Test Resort</td>") {
		t.Errorf("expected Test Resort row restored after reset, got:\n%s", got)
	}
}

// TestTripFilterPanel_ScopedURLsAndSwitch verifies the per-trip filter panel
// renders scoped POST URLs, the scope-aware title, and the inherit/override
// switch — not the global URLs.
func TestTripFilterPanel_ScopedURLsAndSwitch(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	// Switch to override so editable rows (and their scoped URLs) render.
	if _, err := http.PostForm(ts.URL+"/trips/0/filters/mode", url.Values{"mode": {"override"}}); err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(ts.URL + "/trips/0/filters")
	if err != nil {
		t.Fatal(err)
	}
	got := body(t, resp)

	if !strings.Contains(got, `/trips/0/filters/resorts/TST`) {
		t.Errorf("expected scoped resort URL, got:\n%s", got)
	}
	if !strings.Contains(got, "Filters — Trip 1") {
		t.Errorf("expected scope-aware title 'Filters — Trip 1', got:\n%s", got)
	}
	if !strings.Contains(got, `/trips/0/filters/mode`) {
		t.Errorf("expected inherit/override switch posting to mode URL, got:\n%s", got)
	}
}

// TestTripFilterPanel_InheritDisablesRows verifies an inherit trip's panel shows
// disabled rows plus a switch-to-override control, and no editable toggle URLs.
func TestTripFilterPanel_InheritDisablesRows(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/trips/0/filters")
	if err != nil {
		t.Fatal(err)
	}
	got := body(t, resp)

	if !strings.Contains(got, `data-mode="inherit"`) {
		t.Fatalf("expected inherit mode by default, got:\n%s", got)
	}
	if !strings.Contains(got, "disabled") {
		t.Errorf("expected disabled rows on inherit, got:\n%s", got)
	}
	if !strings.Contains(got, "Switch to override to edit") {
		t.Errorf("expected switch-to-override hint, got:\n%s", got)
	}
	// Inherit rows must not be editable toggles.
	if strings.Contains(got, `/trips/0/filters/resorts/TST`) {
		t.Errorf("inherit rows should not POST toggle URLs, got:\n%s", got)
	}
}

// TestTripFilterPanel_OverrideEditableWithChip verifies an override trip's panel
// shows editable rows (toggle URLs) and the override chip/marker.
func TestTripFilterPanel_OverrideEditableWithChip(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	// Switch trip 0 to override.
	if _, err := http.PostForm(ts.URL+"/trips/0/filters/mode", url.Values{"mode": {"override"}}); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Get(ts.URL + "/trips/0/filters")
	if err != nil {
		t.Fatal(err)
	}
	got := body(t, resp)

	if !strings.Contains(got, `data-mode="override"`) {
		t.Fatalf("expected override mode, got:\n%s", got)
	}
	if !strings.Contains(got, `/trips/0/filters/resorts/TST`) {
		t.Errorf("expected editable toggle URL on override, got:\n%s", got)
	}
	if !strings.Contains(got, "override") {
		t.Errorf("expected override marker in panel, got:\n%s", got)
	}
}

// TestTripCard_FiltersButtonAndChip verifies the expanded trip card renders the
// per-trip Filters button and an inherit chip by default, override after switch.
func TestTripCard_FiltersButtonAndChip(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	got := body(t, resp)

	if !strings.Contains(got, `hx-get="/trips/0/filters"`) {
		t.Errorf("expected per-trip Filters button, got:\n%s", got)
	}
	if !strings.Contains(got, "[filters: inherit]") {
		t.Errorf("expected inherit chip by default, got:\n%s", got)
	}

	// Switch to override and re-render the trip card via collapse toggle path.
	if _, err := http.PostForm(ts.URL+"/trips/0/filters/mode", url.Values{"mode": {"override"}}); err != nil {
		t.Fatal(err)
	}
	resp2, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	got2 := body(t, resp2)
	if !strings.Contains(got2, "[filters: override]") {
		t.Errorf("expected override chip after switching mode, got:\n%s", got2)
	}
}

// TestTripCard_CollapsedShowsChip verifies the collapsed trip_summary surfaces
// the override chip too.
func TestTripCard_CollapsedShowsChip(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	// Switch to override, then collapse the trip.
	if _, err := http.PostForm(ts.URL+"/trips/0/filters/mode", url.Values{"mode": {"override"}}); err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(ts.URL+"/trips/0/collapse", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	got := body(t, resp)
	if !strings.Contains(got, "trip collapsed") {
		t.Fatalf("expected collapsed trip, got:\n%s", got)
	}
	if !strings.Contains(got, "[filters: override]") {
		t.Errorf("expected override chip in collapsed summary, got:\n%s", got)
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

// TestSavePlanAndLoad_RoundTripsOverrideTrip verifies a saved plan with one
// override trip round-trips through SavePlan -> LoadPlan: after loading, the
// trip is still override (asserted via the rendered per-trip panel) and its
// per-trip exclusion is preserved. Uses t.TempDir() plans path via the helper.
func TestSavePlanAndLoad_RoundTripsOverrideTrip(t *testing.T) {
	ts := newTestServerWithCharts(t, twoResortCharts())
	defer ts.Close()

	// Make trip 0 an override trip that excludes BTA.
	if _, err := http.Post(ts.URL+"/trips/0/filters/resorts/BTA", "", nil); err != nil {
		t.Fatal(err)
	}
	// Save the plan with that override.
	if _, err := http.PostForm(ts.URL+"/plans", url.Values{"name": {"with-override"}}); err != nil {
		t.Fatal(err)
	}

	// Reset trip 0 back to inherit to prove load actually restores override.
	req, _ := http.NewRequest("DELETE", ts.URL+"/trips/0/filters", nil)
	if _, err := ts.Client().Do(req); err != nil {
		t.Fatal(err)
	}
	pre, err := http.Get(ts.URL + "/trips/0/filters")
	if err != nil {
		t.Fatal(err)
	}
	if got := body(t, pre); !strings.Contains(got, `data-mode="inherit"`) {
		t.Fatalf("expected inherit before load, got:\n%s", got)
	}

	// Load the saved plan; trip 0 must come back as override.
	if _, err := http.Post(ts.URL+"/plans/with-override/load", "", nil); err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(ts.URL + "/trips/0/filters")
	if err != nil {
		t.Fatal(err)
	}
	panel := body(t, resp)
	if !strings.Contains(panel, `data-mode="override"`) {
		t.Errorf("expected loaded trip to be override, got:\n%s", panel)
	}
	// The override's BTA exclusion survived the round-trip.
	assertResortExcluded(t, panel, "Beta Resort")
	if !strings.Contains(panel, "[✓] Alpha Resort") {
		t.Errorf("expected Alpha Resort still enabled in loaded override, got:\n%s", panel)
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
