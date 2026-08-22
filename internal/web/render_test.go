package web

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/lineleader/lineleader/internal/dvc"
	"github.com/lineleader/lineleader/internal/ledger"
)

// newTestSession builds a Session backed by a Planner over the minimal chart, so
// tests can drive Planner mutators and inspect buildAppView directly.
func newTestSession(t *testing.T) *Session {
	t.Helper()
	dir := t.TempDir()
	return NewSession(
		[]*dvc.ResortChart{minimalChart()},
		dvc.Config{},
		filepath.Join(dir, "config.json"),
		Defaults{
			From:      "2026-01-04",
			To:        "2026-01-08",
			Budget:    "100",
			MinNights: "1",
		},
		newCostProvider(nil),
	)
}

// newTestSessionWithStore builds a Session identically to newTestSession but
// backed by a real ledger.Store's costProvider (rather than the nil-store
// one every other render_test.go helper uses), so buildAppView has cost
// data to price results and selections against.
func newTestSessionWithStore(t *testing.T, store *ledger.Store) *Session {
	t.Helper()
	dir := t.TempDir()
	return NewSession(
		[]*dvc.ResortChart{minimalChart()},
		dvc.Config{},
		filepath.Join(dir, "config.json"),
		Defaults{
			From:      "2026-01-04",
			To:        "2026-01-08",
			Budget:    "100",
			MinNights: "1",
		},
		newCostProvider(store),
	)
}

// addPricedContract adds a contract carrying enough cost data
// (PricePerPointYear known) that CostBasis.Known() is true, given seed.sql's
// dues rates are already present on any freshly opened store. The exact
// rate isn't asserted by callers — only that some non-empty $ cost appears.
func addPricedContract(t *testing.T, store *ledger.Store) {
	t.Helper()
	if _, err := store.AddContract(ledger.Contract{
		Name:          "C1",
		AnnualPoints:  100,
		UseYearMonth:  time.January,
		TermYears:     10,
		PurchasePrice: 100_000_00, // $100,000.00
	}); err != nil {
		t.Fatalf("AddContract: %v", err)
	}
}

// An inherit trip projects UsesOverride==false and the inherit FilterMode; an
// override trip projects UsesOverride==true and FilterModeOverride with its own
// Filters.
func TestBuildAppView_PerTripFilterFields(t *testing.T) {
	s := newTestSession(t)
	s.p.AddTrip() // now two trips, both inherit

	// Trip 1 overrides and excludes a resort.
	s.p.ToggleTripResort(1, "TST")
	s.reconcileCollapsed(s.p.Snapshot())

	v := s.buildAppView(s.p.Snapshot())
	if len(v.Trips) != 2 {
		t.Fatalf("len(Trips) = %d, want 2", len(v.Trips))
	}

	t0 := v.Trips[0]
	if t0.UsesOverride {
		t.Errorf("trip 0 UsesOverride = true, want false")
	}
	if t0.FilterMode != dvc.FilterModeInherit {
		t.Errorf("trip 0 FilterMode = %q, want inherit", t0.FilterMode)
	}
	if len(t0.Filters.ExcludeResorts) != 0 || len(t0.Filters.ExcludeRoomTypes) != 0 {
		t.Errorf("trip 0 Filters = %+v, want empty", t0.Filters)
	}

	t1 := v.Trips[1]
	if !t1.UsesOverride {
		t.Errorf("trip 1 UsesOverride = false, want true")
	}
	if t1.FilterMode != dvc.FilterModeOverride {
		t.Errorf("trip 1 FilterMode = %q, want override", t1.FilterMode)
	}
	want := []string{"TST"}
	if got := t1.Filters.ExcludeResorts; len(got) != 1 || got[0] != want[0] {
		t.Errorf("trip 1 Filters.ExcludeResorts = %v, want %v", got, want)
	}
}

// A global FilterOptionsView (TripIndex == -1) projects a non-trip scope.
func TestToFiltersView_GlobalScope(t *testing.T) {
	fv := toFiltersView(dvc.FilterOptionsView{
		TripIndex: -1,
		Resorts:   []dvc.ResortOption{{Code: "TST", Name: "Test Resort", Enabled: true}},
		RoomTypes: []dvc.RoomTypeOption{{Name: "Studio", Enabled: false}},
	})
	if fv.Scope.IsTrip {
		t.Errorf("Scope.IsTrip = true, want false for global")
	}
	if len(fv.Resorts) != 1 || fv.Resorts[0].Code != "TST" || fv.Resorts[0].Name != "Test Resort" || !fv.Resorts[0].Enabled {
		t.Errorf("Resorts = %+v, want one enabled TST/Test Resort", fv.Resorts)
	}
	if len(fv.RoomTypes) != 1 || fv.RoomTypes[0].Name != "Studio" || fv.RoomTypes[0].Enabled {
		t.Errorf("RoomTypes = %+v, want one disabled Studio", fv.RoomTypes)
	}
}

// A trip FilterOptionsView projects a trip scope carrying TripIndex and Mode.
func TestToFiltersView_TripScope(t *testing.T) {
	fv := toFiltersView(dvc.FilterOptionsView{
		TripIndex: 1,
		Mode:      dvc.FilterModeOverride,
		Resorts:   []dvc.ResortOption{{Code: "TST", Name: "Test Resort", Enabled: false}},
		RoomTypes: []dvc.RoomTypeOption{{Name: "Studio", Enabled: true}},
	})
	if !fv.Scope.IsTrip {
		t.Errorf("Scope.IsTrip = false, want true for trip")
	}
	if fv.Scope.TripIndex != 1 {
		t.Errorf("Scope.TripIndex = %d, want 1", fv.Scope.TripIndex)
	}
	if fv.Scope.Mode != dvc.FilterModeOverride {
		t.Errorf("Scope.Mode = %q, want override", fv.Scope.Mode)
	}
	if len(fv.Resorts) != 1 || fv.Resorts[0].Enabled {
		t.Errorf("Resorts = %+v, want one disabled resort (Enabled intact)", fv.Resorts)
	}
	if len(fv.RoomTypes) != 1 || !fv.RoomTypes[0].Enabled {
		t.Errorf("RoomTypes = %+v, want one enabled room type (Enabled intact)", fv.RoomTypes)
	}
}

// Budget/remaining/selection projection from the Snapshot is preserved.
func TestBuildAppView_BudgetRemainingSelection(t *testing.T) {
	s := newTestSession(t)
	s.reconcileCollapsed(s.p.Snapshot())

	v := s.buildAppView(s.p.Snapshot())
	if v.Budget != "100" {
		t.Errorf("Budget = %q, want %q", v.Budget, "100")
	}
	if v.BudgetErr != "" {
		t.Errorf("BudgetErr = %q, want empty", v.BudgetErr)
	}
	if v.Remaining != 100 {
		t.Errorf("Remaining = %d, want 100 (no selection yet)", v.Remaining)
	}

	// Select the first result row of trip 0 and confirm it flows through.
	s.p.ToggleSelection(0, 0)
	v = s.buildAppView(s.p.Snapshot())
	t0 := v.Trips[0]
	if !t0.HasSelection {
		t.Fatalf("trip 0 HasSelection = false, want true")
	}
	if t0.Selected == nil {
		t.Fatalf("trip 0 Selected = nil, want a row")
	}
	if !t0.Selected.Selected {
		t.Errorf("trip 0 Selected.Selected = false, want true")
	}
	if v.Remaining != 100-t0.Selected.Points {
		t.Errorf("Remaining = %d, want %d", v.Remaining, 100-t0.Selected.Points)
	}
}

// TestBuildAppView_NoLedgerHidesCosts pins the nil-store byte-identical
// contract: with no ledger configured (the newTestSession helper every
// other test in this file uses), ShowCosts must be false everywhere and no
// CostLabel ever gets populated — this is what lets every pre-existing
// planner test go on rendering the same HTML with no edits.
func TestBuildAppView_NoLedgerHidesCosts(t *testing.T) {
	s := newTestSession(t)
	s.reconcileCollapsed(s.p.Snapshot())

	v := s.buildAppView(s.p.Snapshot())
	if v.ShowCosts {
		t.Errorf("appView ShowCosts = true, want false with no ledger configured")
	}
	if v.SelectedCostLabel != "" {
		t.Errorf("appView SelectedCostLabel = %q, want empty with no ledger configured", v.SelectedCostLabel)
	}
	for _, tr := range v.Trips {
		if tr.ShowCosts {
			t.Errorf("trip ShowCosts = true, want false with no ledger configured")
		}
		if tr.SelectedCostLabel != "" {
			t.Errorf("trip SelectedCostLabel = %q, want empty with no ledger configured", tr.SelectedCostLabel)
		}
		for _, r := range tr.Results {
			if r.CostLabel != "" {
				t.Errorf("result CostLabel = %q, want empty with no ledger configured", r.CostLabel)
			}
		}
	}
}

// TestBuildAppView_PricesResultsAndSelection confirms a Session backed by a
// priced ledger (a contract with cost data, seed.sql's dues rates already
// present) prices every result row and, once a stay is selected, the trip's
// SelectedCostLabel.
func TestBuildAppView_PricesResultsAndSelection(t *testing.T) {
	store := ledger.OpenTest(t)
	addPricedContract(t, store)

	s := newTestSessionWithStore(t, store)
	s.reconcileCollapsed(s.p.Snapshot())

	v := s.buildAppView(s.p.Snapshot())
	t0 := v.Trips[0]
	if !t0.ShowCosts {
		t.Fatalf("trip ShowCosts = false, want true (contract priced, dues seeded)")
	}
	if len(t0.Results) == 0 {
		t.Fatalf("no results to price against the minimal chart")
	}
	for _, r := range t0.Results {
		if r.CostLabel == "" {
			t.Errorf("result row CostLabel empty, want a priced $ label for %+v", r)
		}
	}

	s.p.ToggleSelection(0, 0)
	v = s.buildAppView(s.p.Snapshot())
	t0 = v.Trips[0]
	if t0.SelectedCostLabel == "" {
		t.Errorf("trip SelectedCostLabel empty after selection, want a priced $ label")
	}
	if t0.Selected == nil || t0.Selected.CostLabel != t0.SelectedCostLabel {
		t.Errorf("trip SelectedCostLabel = %q, want to match Selected.CostLabel = %q", t0.SelectedCostLabel, t0.Selected.CostLabel)
	}
	if !v.ShowCosts {
		t.Errorf("appView ShowCosts = false, want true")
	}
	if v.SelectedCostLabel != t0.Selected.CostLabel {
		t.Errorf("appView SelectedCostLabel = %q, want to match the lone selection's cost %q", v.SelectedCostLabel, t0.Selected.CostLabel)
	}
}

// TestBuildAppView_SelectionMatchesOnPointsToo reproduces a real DVC search
// collision: two rows identical in resort/room type/view/check-in/check-out
// but different Points (e.g. a studio bookable at both a weekday and a
// weekend point cost). Before stayKey included Points, both rows matched the
// selected stay's key, so both rendered the checkmark and the result loop's
// "last match wins" left tv.Selected pointing at the wrong (unselected) row —
// its Points, and therefore its priced cost, disagreed with the actually
// selected stay. stayKey must include Points so a selection identifies
// exactly one row.
func TestBuildAppView_SelectionMatchesOnPointsToo(t *testing.T) {
	store := ledger.OpenTest(t)
	addPricedContract(t, store)
	s := newTestSessionWithStore(t, store)

	checkIn := time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC)
	checkOut := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	cheap := dvc.StayResult{
		Resort: "Disney's Animal Kingdom Villas", RoomType: "DELUXE STUDIO", View: "V",
		CheckIn: checkIn, CheckOut: checkOut, Nights: 1, Points: 9,
	}
	pricey := dvc.StayResult{
		Resort: "Disney's Animal Kingdom Villas", RoomType: "DELUXE STUDIO", View: "V",
		CheckIn: checkIn, CheckOut: checkOut, Nights: 1, Points: 21,
	}
	selected := cheap
	snap := dvc.Snapshot{
		Budget:    "100",
		Remaining: 100 - cheap.Points,
		Trips: []dvc.TripSnapshot{
			{
				Results:  []dvc.StayResult{cheap, pricey},
				Selected: &selected,
			},
		},
	}

	v := s.buildAppView(snap)
	t0 := v.Trips[0]

	selectedCount := 0
	for _, r := range t0.Results {
		if r.Selected {
			selectedCount++
		}
	}
	if selectedCount != 1 {
		t.Fatalf("selectedCount = %d, want exactly 1 row marked Selected", selectedCount)
	}

	if t0.Selected == nil {
		t.Fatalf("trip Selected = nil, want the cheap (9 pt) row")
	}
	if t0.Selected.Points != cheap.Points {
		t.Errorf("trip Selected.Points = %d, want %d (the actually-selected row's points)", t0.Selected.Points, cheap.Points)
	}

	// Find each row's own priced CostLabel to confirm SelectedCostLabel
	// tracks the cheap row, not the pricey collider.
	var cheapLabel, priceyLabel string
	for _, r := range t0.Results {
		switch r.Points {
		case cheap.Points:
			cheapLabel = r.CostLabel
		case pricey.Points:
			priceyLabel = r.CostLabel
		}
	}
	if cheapLabel == "" || priceyLabel == "" {
		t.Fatalf("expected both colliding rows to be priced: cheap=%q pricey=%q", cheapLabel, priceyLabel)
	}
	if cheapLabel == priceyLabel {
		t.Fatalf("cheap and pricey rows priced identically (%q); test can't distinguish them", cheapLabel)
	}
	if t0.SelectedCostLabel != cheapLabel {
		t.Errorf("trip SelectedCostLabel = %q, want the cheap row's cost %q (got the collider's %q)", t0.SelectedCostLabel, cheapLabel, priceyLabel)
	}
}

// TestBuildAppView_SumsSelectedCostAcrossTrips confirms appView's
// SelectedCostLabel sums real cents across every trip's selection (see
// resultRow.cost) and formats once at the end, rather than concatenating or
// re-parsing each trip's own formatted CostLabel.
func TestBuildAppView_SumsSelectedCostAcrossTrips(t *testing.T) {
	store := ledger.OpenTest(t)
	addPricedContract(t, store)

	s := newTestSessionWithStore(t, store)
	s.p.AddTrip() // trip 1, same defaults (search auto-runs), so it has its own row 0
	s.reconcileCollapsed(s.p.Snapshot())

	s.p.ToggleSelection(0, 0)
	s.p.ToggleSelection(1, 0)
	v := s.buildAppView(s.p.Snapshot())

	if len(v.Trips) != 2 {
		t.Fatalf("len(Trips) = %d, want 2", len(v.Trips))
	}
	t0, t1 := v.Trips[0], v.Trips[1]
	if t0.Selected == nil || t1.Selected == nil {
		t.Fatalf("expected both trips to have a selection: t0=%v t1=%v", t0.Selected, t1.Selected)
	}
	wantSum := t0.Selected.cost + t1.Selected.cost
	if wantSum <= 0 {
		t.Fatalf("wantSum = %v, want > 0 (both selections should be priced)", wantSum)
	}
	if want := ledger.FormatUSD(wantSum); v.SelectedCostLabel != want {
		t.Errorf("appView SelectedCostLabel = %q, want %q (sum of both trips' selected cents, formatted once)", v.SelectedCostLabel, want)
	}
}

// TestBuildAppView_HasSelectedCost confirms appView.HasSelectedCost — the
// flag the planner bar template uses to decide whether to render its
// selected-cost span at all — is false with a priced ledger configured but
// nothing selected (never "$0.00" noise), and true once a stay is selected.
// It must not be inferred from SelectedCostLabel's formatted text (e.g.
// != "" or != "$0.00"); it's set directly by whether a trip contributed a
// priced selection.
func TestBuildAppView_HasSelectedCost(t *testing.T) {
	store := ledger.OpenTest(t)
	addPricedContract(t, store)
	s := newTestSessionWithStore(t, store)
	s.reconcileCollapsed(s.p.Snapshot())

	v := s.buildAppView(s.p.Snapshot())
	if v.HasSelectedCost {
		t.Errorf("appView HasSelectedCost = true, want false with nothing selected")
	}

	s.p.ToggleSelection(0, 0)
	v = s.buildAppView(s.p.Snapshot())
	if !v.HasSelectedCost {
		t.Errorf("appView HasSelectedCost = false, want true after selecting a priced stay")
	}
	if v.SelectedCostLabel == "" {
		t.Errorf("appView SelectedCostLabel empty, want a priced $ label once HasSelectedCost is true")
	}
}
