package web

import (
	"path/filepath"
	"testing"

	"github.com/lineleader/lineleader/internal/dvc"
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
		nil,
		filepath.Join(dir, "plans.json"),
		Defaults{
			From:      "2026-01-04",
			To:        "2026-01-08",
			Budget:    "100",
			MinNights: "1",
		},
	)
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
