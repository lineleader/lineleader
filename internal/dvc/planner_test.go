package dvc

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

func TestEffectiveFilters_InheritIgnoresTripFilters(t *testing.T) {
	global := Config{
		ExcludeResorts:   []string{"VERO", "HH"},
		ExcludeRoomTypes: []string{"THREE-BEDROOM GRAND VILLA"},
	}
	trip := FilterSet{
		ExcludeResorts:   []string{"AKV"},
		ExcludeRoomTypes: []string{"RESORT STUDIO"},
	}

	got := EffectiveFilters(global, FilterModeInherit, trip)

	want := global.AsFilterSet()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("inherit: got %+v, want global %+v", got, want)
	}
}

func TestEffectiveFilters_OverrideReturnsTripFilters(t *testing.T) {
	global := Config{
		ExcludeResorts:   []string{"VERO", "HH"},
		ExcludeRoomTypes: []string{"THREE-BEDROOM GRAND VILLA"},
	}
	trip := FilterSet{
		ExcludeResorts:   []string{"AKV"},
		ExcludeRoomTypes: []string{"RESORT STUDIO"},
	}

	got := EffectiveFilters(global, FilterModeOverride, trip)

	if !reflect.DeepEqual(got, trip) {
		t.Errorf("override: got %+v, want trip %+v", got, trip)
	}
}

func TestEffectiveFilters_OverrideWithEmptyIgnoresGlobal(t *testing.T) {
	global := Config{
		ExcludeResorts:   []string{"VERO", "HH"},
		ExcludeRoomTypes: []string{"THREE-BEDROOM GRAND VILLA"},
	}

	got := EffectiveFilters(global, FilterModeOverride, FilterSet{})

	if len(got.ExcludeResorts) != 0 || len(got.ExcludeRoomTypes) != 0 {
		t.Errorf("override with empty set should yield empty exclusions, got %+v", got)
	}
}

// --- Planner ---

// newTestPlanner builds a Planner over the minimal test chart with one trip
// covering Jan 4–8 2026 and the given budget.
func newTestPlanner(budget string) *Planner {
	return NewPlanner(PlannerOptions{
		Charts: []*ResortChart{minimalChart()},
		Defaults: Defaults{
			From:      "2026-01-04",
			To:        "2026-01-08",
			Budget:    budget,
			MinNights: "1",
		},
	})
}

func TestNewPlanner_SeedsOneTripWithResults(t *testing.T) {
	p := newTestPlanner("200")
	if len(p.trips) != 1 {
		t.Fatalf("trips = %d, want 1", len(p.trips))
	}
	tr := p.trips[0]
	if tr.Err != "" {
		t.Fatalf("unexpected trip error: %s", tr.Err)
	}
	if len(tr.Results) == 0 {
		t.Error("expected seeded trip to have results, got none")
	}
	if p.budget != "200" {
		t.Errorf("budget = %q, want %q", p.budget, "200")
	}
	if got := tr.Fields[0].value; got != "2026-01-04" {
		t.Errorf("From = %q, want 2026-01-04", got)
	}
	if got := tr.Fields[2].value; got != "1" {
		t.Errorf("MinNights = %q, want 1", got)
	}
}

func TestSetBudget_InvalidMarksAllTrips(t *testing.T) {
	p := newTestPlanner("200")
	p.AddTrip()

	p.SetBudget("")
	for i, tr := range p.trips {
		if tr.Err != "invalid Budget" {
			t.Errorf("trip %d: Err = %q, want %q", i, tr.Err, "invalid Budget")
		}
		if tr.Results != nil {
			t.Errorf("trip %d: Results = %d, want nil", i, len(tr.Results))
		}
	}

	p.SetBudget("not-a-number")
	for i, tr := range p.trips {
		if tr.Err != "invalid Budget" {
			t.Errorf("trip %d (non-numeric): Err = %q, want %q", i, tr.Err, "invalid Budget")
		}
	}
}

func TestSetBudget_ValidRecomputes(t *testing.T) {
	p := newTestPlanner("")
	if p.trips[0].Err != "invalid Budget" {
		t.Fatalf("precondition: expected invalid budget, got %q", p.trips[0].Err)
	}
	p.SetBudget("200")
	if p.trips[0].Err != "" {
		t.Errorf("Err = %q, want empty after valid budget", p.trips[0].Err)
	}
	if len(p.trips[0].Results) == 0 {
		t.Error("expected results after valid budget")
	}
}

func TestAddTrip_ClonesDatesAndResetsMinNights(t *testing.T) {
	p := newTestPlanner("200")
	p.AddTrip()
	if len(p.trips) != 2 {
		t.Fatalf("trips = %d, want 2", len(p.trips))
	}
	newTrip := p.trips[1]
	if newTrip.Fields[0].value != "2026-01-04" {
		t.Errorf("new trip From = %q, want clone 2026-01-04", newTrip.Fields[0].value)
	}
	if newTrip.Fields[1].value != "2026-01-08" {
		t.Errorf("new trip To = %q, want clone 2026-01-08", newTrip.Fields[1].value)
	}
	if newTrip.Fields[2].value != "1" {
		t.Errorf("new trip MinNights = %q, want 1", newTrip.Fields[2].value)
	}
}

func TestRemoveTrip(t *testing.T) {
	p := newTestPlanner("200")
	// No-op with one trip.
	p.RemoveTrip(0)
	if len(p.trips) != 1 {
		t.Fatalf("RemoveTrip on single trip changed count to %d", len(p.trips))
	}
	// Out of range no-op.
	p.AddTrip()
	p.RemoveTrip(5)
	if len(p.trips) != 2 {
		t.Fatalf("RemoveTrip out of range changed count to %d", len(p.trips))
	}
	// Valid removal.
	p.RemoveTrip(0)
	if len(p.trips) != 1 {
		t.Fatalf("RemoveTrip valid: count = %d, want 1", len(p.trips))
	}
}

func TestSetTripField(t *testing.T) {
	p := newTestPlanner("200")
	p.SetTripField(0, 2, "4")
	if p.trips[0].Fields[2].value != "4" {
		t.Errorf("MinNights = %q, want 4", p.trips[0].Fields[2].value)
	}
	// Out of range trip is a no-op (must not panic).
	p.SetTripField(9, 0, "x")
}

func TestToggleSelection_TogglesAndAffectsOtherTrip(t *testing.T) {
	p := newTestPlanner("200")
	p.AddTrip()
	if len(p.trips[0].Results) == 0 {
		t.Skip("no results for trip 0")
	}
	before := len(p.trips[1].Results)

	// Select trip 0's most expensive result.
	mostExpensive := len(p.trips[0].Results) - 1
	wantPts := p.trips[0].Results[mostExpensive].Points
	p.ToggleSelection(0, mostExpensive)

	if p.trips[0].Selected == nil {
		t.Fatal("trip 0 Selected is nil after toggle")
	}
	if p.trips[0].Selected.Points != wantPts {
		t.Errorf("selected points = %d, want %d", p.trips[0].Selected.Points, wantPts)
	}
	if wantPts > 0 && len(p.trips[1].Results) > before {
		t.Errorf("trip 1 results grew after trip 0 selection: before=%d after=%d", before, len(p.trips[1].Results))
	}

	// Re-toggle same row deselects.
	p.ToggleSelection(0, mostExpensive)
	if p.trips[0].Selected != nil {
		t.Error("trip 0 Selected not cleared after re-toggle")
	}
	if len(p.trips[1].Results) != before {
		t.Errorf("trip 1 results not restored after deselect: got %d, want %d", len(p.trips[1].Results), before)
	}
}

func TestToggleSelection_OutOfRange(t *testing.T) {
	p := newTestPlanner("200")
	p.ToggleSelection(9, 0)    // out-of-range trip
	p.ToggleSelection(0, 9999) // out-of-range row
	if p.trips[0].Selected != nil {
		t.Error("Selected set by out-of-range toggle")
	}
}

func TestRecompute_InheritResolvesGlobalFilters(t *testing.T) {
	p := NewPlanner(PlannerOptions{
		Charts: []*ResortChart{minimalChart()},
		Global: Config{ExcludeResorts: []string{"TST"}},
		Defaults: Defaults{
			From:      "2026-01-04",
			To:        "2026-01-08",
			Budget:    "200",
			MinNights: "1",
		},
	})
	// The only resort (TST) is globally excluded; an inherit trip should see none.
	if len(p.trips[0].Results) != 0 {
		t.Errorf("inherit trip ignored global exclusion: got %d results", len(p.trips[0].Results))
	}
}

func TestRecompute_OverrideIgnoresGlobalFilters(t *testing.T) {
	p := NewPlanner(PlannerOptions{
		Charts: []*ResortChart{minimalChart()},
		Global: Config{ExcludeResorts: []string{"TST"}},
		Defaults: Defaults{
			From:      "2026-01-04",
			To:        "2026-01-08",
			Budget:    "200",
			MinNights: "1",
		},
	})
	// Switch trip 0 to override with empty filters: global exclusion no longer applies.
	p.trips[0].FilterMode = FilterModeOverride
	p.trips[0].Filters = FilterSet{}
	p.recomputeAll()
	if len(p.trips[0].Results) == 0 {
		t.Error("override trip with empty filters should ignore global exclusion and return results")
	}
}

// --- Global filter toggles ---

// newGlobalFilterPlanner builds a Planner with configPath under t.TempDir and
// two trips: trip 0 inherits global filters, trip 1 overrides with an empty
// filter set so global toggles never affect it.
func newGlobalFilterPlanner(t *testing.T) *Planner {
	t.Helper()
	p := NewPlanner(PlannerOptions{
		Charts:     []*ResortChart{minimalChart()},
		ConfigPath: filepath.Join(t.TempDir(), "config.json"),
		Defaults: Defaults{
			From:      "2026-01-04",
			To:        "2026-01-08",
			Budget:    "200",
			MinNights: "1",
		},
	})
	// Add a second trip pinned to OVERRIDE with empty filters.
	p.trips = append(p.trips, Trip{
		Fields: [3]inputField{
			{label: "From", value: "2026-01-04"},
			{label: "To", value: "2026-01-08"},
			{label: "Min nights", value: "1"},
		},
		FilterMode: FilterModeOverride,
		Filters:    FilterSet{},
	})
	p.recomputeAll()
	return p
}

func TestToggleGlobalResort_RoundTripAndPersists(t *testing.T) {
	p := newGlobalFilterPlanner(t)

	if err := p.ToggleGlobalResort("TST"); err != nil {
		t.Fatalf("ToggleGlobalResort add: %v", err)
	}
	if !slices.Contains(p.global.ExcludeResorts, "TST") {
		t.Errorf("after add: ExcludeResorts = %v, want to contain TST", p.global.ExcludeResorts)
	}

	// Persisted to disk.
	cfg, err := LoadConfig(p.configPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !slices.Contains(cfg.ExcludeResorts, "TST") {
		t.Errorf("saved config ExcludeResorts = %v, want to contain TST", cfg.ExcludeResorts)
	}

	// Second toggle removes it (round-trip).
	if err := p.ToggleGlobalResort("TST"); err != nil {
		t.Fatalf("ToggleGlobalResort remove: %v", err)
	}
	if slices.Contains(p.global.ExcludeResorts, "TST") {
		t.Errorf("after remove: ExcludeResorts = %v, want TST gone", p.global.ExcludeResorts)
	}
	cfg, err = LoadConfig(p.configPath)
	if err != nil {
		t.Fatalf("LoadConfig after remove: %v", err)
	}
	if slices.Contains(cfg.ExcludeResorts, "TST") {
		t.Errorf("saved config after remove still has TST: %v", cfg.ExcludeResorts)
	}
}

func TestToggleGlobalResort_AffectsInheritNotOverride(t *testing.T) {
	p := newGlobalFilterPlanner(t)

	inheritBefore := len(p.trips[0].Results)
	overrideBefore := len(p.trips[1].Results)
	if inheritBefore == 0 || overrideBefore == 0 {
		t.Fatalf("precondition: need results, got inherit=%d override=%d", inheritBefore, overrideBefore)
	}

	if err := p.ToggleGlobalResort("TST"); err != nil {
		t.Fatalf("ToggleGlobalResort: %v", err)
	}

	// Inherit trip drops the now-excluded resort (only TST exists -> 0 results).
	if len(p.trips[0].Results) != 0 {
		t.Errorf("inherit trip kept excluded resort: got %d results, want 0", len(p.trips[0].Results))
	}
	// Override trip is unchanged.
	if len(p.trips[1].Results) != overrideBefore {
		t.Errorf("override trip changed: before=%d after=%d", overrideBefore, len(p.trips[1].Results))
	}
}

func TestToggleGlobalRoomType_RoundTripAndPersists(t *testing.T) {
	p := newGlobalFilterPlanner(t)

	if err := p.ToggleGlobalRoomType("STUDIO"); err != nil {
		t.Fatalf("ToggleGlobalRoomType add: %v", err)
	}
	if !slices.Contains(p.global.ExcludeRoomTypes, "STUDIO") {
		t.Errorf("after add: ExcludeRoomTypes = %v, want to contain STUDIO", p.global.ExcludeRoomTypes)
	}
	cfg, err := LoadConfig(p.configPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !slices.Contains(cfg.ExcludeRoomTypes, "STUDIO") {
		t.Errorf("saved config ExcludeRoomTypes = %v, want to contain STUDIO", cfg.ExcludeRoomTypes)
	}

	if err := p.ToggleGlobalRoomType("STUDIO"); err != nil {
		t.Fatalf("ToggleGlobalRoomType remove: %v", err)
	}
	if slices.Contains(p.global.ExcludeRoomTypes, "STUDIO") {
		t.Errorf("after remove: ExcludeRoomTypes = %v, want STUDIO gone", p.global.ExcludeRoomTypes)
	}
}

func TestToggleGlobalRoomType_AffectsInheritNotOverride(t *testing.T) {
	p := newGlobalFilterPlanner(t)

	overrideBefore := len(p.trips[1].Results)
	if len(p.trips[0].Results) == 0 || overrideBefore == 0 {
		t.Fatalf("precondition: need results")
	}

	if err := p.ToggleGlobalRoomType("STUDIO"); err != nil {
		t.Fatalf("ToggleGlobalRoomType: %v", err)
	}

	// Only room type is STUDIO -> inherit trip has no results.
	if len(p.trips[0].Results) != 0 {
		t.Errorf("inherit trip kept excluded room type: got %d results, want 0", len(p.trips[0].Results))
	}
	if len(p.trips[1].Results) != overrideBefore {
		t.Errorf("override trip changed: before=%d after=%d", overrideBefore, len(p.trips[1].Results))
	}
}

func TestToggleGlobalResort_SaveErrorReturnedButStateMutated(t *testing.T) {
	p := newGlobalFilterPlanner(t)
	// Point configPath at a path whose parent is a file, so MkdirAll/Save fails.
	fileAsDir := filepath.Join(t.TempDir(), "notadir")
	if err := os.WriteFile(fileAsDir, []byte("x"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	p.configPath = filepath.Join(fileAsDir, "config.json")

	err := p.ToggleGlobalResort("TST")
	if err == nil {
		t.Fatal("expected SaveConfig error, got nil")
	}
	// In-memory toggle + recompute still happened (not rolled back).
	if !slices.Contains(p.global.ExcludeResorts, "TST") {
		t.Errorf("state rolled back on save error: ExcludeResorts = %v", p.global.ExcludeResorts)
	}
	if len(p.trips[0].Results) != 0 {
		t.Errorf("recompute did not run on save error: got %d results", len(p.trips[0].Results))
	}
}
