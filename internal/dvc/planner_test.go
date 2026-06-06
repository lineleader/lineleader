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

// --- Per-trip filter ops (fpl.6) ---

// twoResortCharts returns two single-room-type resorts (AAA/STUDIO and
// BBB/VILLA) so per-trip filter tests can distinguish resort and room-type
// exclusions independently.
func twoResortCharts() []*ResortChart {
	mk := func(code, room string) *ResortChart {
		return &ResortChart{
			ResortName: code + " Resort",
			ResortCode: code,
			Year:       2026,
			Columns:    []Column{{RoomType: room, View: "R", Sleeps: 4}},
			Seasons: []Season{{
				Periods: []DateRange{{Start: "2026-01-01", End: "2026-01-31"}},
				SunThu:  []int{10},
				FriSat:  []int{14},
			}},
		}
	}
	return []*ResortChart{mk("AAA", "STUDIO"), mk("BBB", "VILLA")}
}

// newPerTripPlanner builds a Planner over twoResortCharts with the given global
// config and two inherit trips covering Jan 4–8 2026.
func newPerTripPlanner(t *testing.T, global Config) *Planner {
	t.Helper()
	p := NewPlanner(PlannerOptions{
		Charts:     twoResortCharts(),
		Global:     global,
		ConfigPath: filepath.Join(t.TempDir(), "config.json"),
		Defaults: Defaults{
			From:      "2026-01-04",
			To:        "2026-01-08",
			Budget:    "200",
			MinNights: "1",
		},
	})
	p.AddTrip()
	return p
}

// resortsInResults returns the set of resort codes present in results.
func resortsInResults(results []StayResult) map[string]bool {
	set := map[string]bool{}
	for _, r := range results {
		set[r.Resort] = true
	}
	return set
}

// (a) ISOLATION: a per-trip toggle on trip 0 never changes trip 1.
func TestToggleTripResort_IsolatedFromOtherTrip(t *testing.T) {
	p := newPerTripPlanner(t, Config{})

	trip1ModeBefore := p.trips[1].FilterMode
	trip1FiltersBefore := cloneFilterSet(p.trips[1].Filters)
	trip1ResultsBefore := len(p.trips[1].Results)

	p.ToggleTripResort(0, "AAA")

	if p.trips[1].FilterMode != trip1ModeBefore {
		t.Errorf("trip 1 FilterMode changed: got %q, want %q", p.trips[1].FilterMode, trip1ModeBefore)
	}
	if !reflect.DeepEqual(p.trips[1].Filters, trip1FiltersBefore) {
		t.Errorf("trip 1 Filters changed: got %+v, want %+v", p.trips[1].Filters, trip1FiltersBefore)
	}
	if len(p.trips[1].Results) != trip1ResultsBefore {
		t.Errorf("trip 1 Results changed: got %d, want %d", len(p.trips[1].Results), trip1ResultsBefore)
	}
}

// (b) SEEDING + NO ALIASING: global excludes AAA; toggling BBB on an inherit
// trip flips it to override, keeps AAA excluded (seeded), adds BBB, and leaves
// the global slice untouched.
func TestToggleTripResort_SeedsFromGlobalWithoutAliasing(t *testing.T) {
	p := newPerTripPlanner(t, Config{ExcludeResorts: []string{"AAA"}})
	globalBefore := append([]string(nil), p.global.ExcludeResorts...)

	p.ToggleTripResort(0, "BBB")

	if p.trips[0].FilterMode != FilterModeOverride {
		t.Errorf("trip 0 FilterMode = %q, want override", p.trips[0].FilterMode)
	}
	if !slices.Contains(p.trips[0].Filters.ExcludeResorts, "AAA") {
		t.Errorf("trip 0 lost seeded exclusion AAA: %v", p.trips[0].Filters.ExcludeResorts)
	}
	if !slices.Contains(p.trips[0].Filters.ExcludeResorts, "BBB") {
		t.Errorf("trip 0 missing toggled exclusion BBB: %v", p.trips[0].Filters.ExcludeResorts)
	}
	// Both resorts now excluded -> no results.
	if len(p.trips[0].Results) != 0 {
		t.Errorf("trip 0 results = %d, want 0 (both resorts excluded)", len(p.trips[0].Results))
	}
	// NO ALIASING: global slice is exactly unchanged.
	if !reflect.DeepEqual(p.global.ExcludeResorts, globalBefore) {
		t.Errorf("global ExcludeResorts mutated by per-trip toggle: got %v, want %v", p.global.ExcludeResorts, globalBefore)
	}
}

// (b') room-type variant of the seeding/aliasing property.
func TestToggleTripRoomType_SeedsFromGlobalWithoutAliasing(t *testing.T) {
	p := newPerTripPlanner(t, Config{ExcludeRoomTypes: []string{"STUDIO"}})
	globalBefore := append([]string(nil), p.global.ExcludeRoomTypes...)

	p.ToggleTripRoomType(0, "VILLA")

	if p.trips[0].FilterMode != FilterModeOverride {
		t.Errorf("trip 0 FilterMode = %q, want override", p.trips[0].FilterMode)
	}
	if !slices.Contains(p.trips[0].Filters.ExcludeRoomTypes, "STUDIO") {
		t.Errorf("trip 0 lost seeded exclusion STUDIO: %v", p.trips[0].Filters.ExcludeRoomTypes)
	}
	if !slices.Contains(p.trips[0].Filters.ExcludeRoomTypes, "VILLA") {
		t.Errorf("trip 0 missing toggled exclusion VILLA: %v", p.trips[0].Filters.ExcludeRoomTypes)
	}
	if !reflect.DeepEqual(p.global.ExcludeRoomTypes, globalBefore) {
		t.Errorf("global ExcludeRoomTypes mutated: got %v, want %v", p.global.ExcludeRoomTypes, globalBefore)
	}
}

// (c) RESET: after ResetTripFilters the trip inherits global again, so a later
// global toggle changes its Results.
func TestResetTripFilters_ReturnsToInheritAndReusesGlobal(t *testing.T) {
	p := newPerTripPlanner(t, Config{})
	// Diverge trip 0 onto override.
	p.ToggleTripResort(0, "AAA")
	if p.trips[0].FilterMode != FilterModeOverride {
		t.Fatalf("precondition: trip 0 should be override, got %q", p.trips[0].FilterMode)
	}

	p.ResetTripFilters(0)
	if p.trips[0].FilterMode != FilterModeInherit {
		t.Errorf("after reset FilterMode = %q, want inherit", p.trips[0].FilterMode)
	}
	if len(p.trips[0].Filters.ExcludeResorts) != 0 || len(p.trips[0].Filters.ExcludeRoomTypes) != 0 {
		t.Errorf("after reset Filters not cleared: %+v", p.trips[0].Filters)
	}

	before := len(p.trips[0].Results)
	// Excluding BBB globally should now shrink the inherit trip's results.
	if err := p.ToggleGlobalResort("BBB"); err != nil {
		t.Fatalf("ToggleGlobalResort: %v", err)
	}
	if len(p.trips[0].Results) >= before {
		t.Errorf("inherit trip did not re-use global after reset: before=%d after=%d", before, len(p.trips[0].Results))
	}
}

// (d) SetTripFilterMode override then inherit clears Filters.
func TestSetTripFilterMode_OverrideThenInheritClearsFilters(t *testing.T) {
	p := newPerTripPlanner(t, Config{ExcludeResorts: []string{"AAA"}})

	p.SetTripFilterMode(0, FilterModeOverride)
	if p.trips[0].FilterMode != FilterModeOverride {
		t.Fatalf("mode = %q, want override", p.trips[0].FilterMode)
	}
	// Override on an inherit trip seeds from global.
	if !slices.Contains(p.trips[0].Filters.ExcludeResorts, "AAA") {
		t.Errorf("override did not seed from global: %v", p.trips[0].Filters.ExcludeResorts)
	}

	p.SetTripFilterMode(0, FilterModeInherit)
	if p.trips[0].FilterMode != FilterModeInherit {
		t.Errorf("mode = %q, want inherit", p.trips[0].FilterMode)
	}
	if len(p.trips[0].Filters.ExcludeResorts) != 0 || len(p.trips[0].Filters.ExcludeRoomTypes) != 0 {
		t.Errorf("inherit did not clear Filters: %+v", p.trips[0].Filters)
	}
}

// SetTripFilterMode(override) on an already-override trip preserves its Filters.
func TestSetTripFilterMode_OverrideOnOverrideDoesNotReseed(t *testing.T) {
	p := newPerTripPlanner(t, Config{ExcludeResorts: []string{"AAA"}})

	p.SetTripFilterMode(0, FilterModeOverride)
	// Diverge: drop the seeded AAA exclusion.
	p.ToggleTripResort(0, "AAA")
	if slices.Contains(p.trips[0].Filters.ExcludeResorts, "AAA") {
		t.Fatalf("precondition: AAA should be toggled off, got %v", p.trips[0].Filters.ExcludeResorts)
	}

	// Re-asserting override must NOT re-seed AAA back in.
	p.SetTripFilterMode(0, FilterModeOverride)
	if slices.Contains(p.trips[0].Filters.ExcludeResorts, "AAA") {
		t.Errorf("override re-seeded an already-override trip: %v", p.trips[0].Filters.ExcludeResorts)
	}
}

// (e) OVERRIDE TO EMPTY: toggling off every exclusion yields all stays in budget.
func TestToggleTripResort_OverrideToEmptyShowsEverything(t *testing.T) {
	p := newPerTripPlanner(t, Config{ExcludeResorts: []string{"AAA", "BBB"}})

	// Seed override (both excluded), then toggle both off.
	p.SetTripFilterMode(0, FilterModeOverride)
	p.ToggleTripResort(0, "AAA")
	p.ToggleTripResort(0, "BBB")

	if len(p.trips[0].Filters.ExcludeResorts) != 0 {
		t.Errorf("expected no resort exclusions, got %v", p.trips[0].Filters.ExcludeResorts)
	}
	// StayResult.Resort holds the resort NAME, not the code.
	got := resortsInResults(p.trips[0].Results)
	if !got["AAA Resort"] || !got["BBB Resort"] {
		t.Errorf("override-to-empty trip missing resorts: got %v, want both AAA Resort and BBB Resort", got)
	}
}

// EDGE: out-of-range i is a no-op (no panic) for every per-trip op.
func TestPerTripOps_OutOfRangeNoOp(t *testing.T) {
	p := newPerTripPlanner(t, Config{})
	p.SetTripFilterMode(9, FilterModeOverride)
	p.SetTripFilterMode(-1, FilterModeOverride)
	p.ToggleTripResort(9, "AAA")
	p.ToggleTripRoomType(-1, "STUDIO")
	p.ResetTripFilters(99)
	// Existing trips untouched.
	if p.trips[0].FilterMode != FilterModeInherit || p.trips[1].FilterMode != FilterModeInherit {
		t.Errorf("out-of-range op mutated a trip: %q %q", p.trips[0].FilterMode, p.trips[1].FilterMode)
	}
}

// EDGE: toggling the same code twice on an override trip returns to the seeded state.
func TestToggleTripResort_TwiceReturnsToSeededState(t *testing.T) {
	p := newPerTripPlanner(t, Config{ExcludeResorts: []string{"AAA"}})
	p.SetTripFilterMode(0, FilterModeOverride)
	seeded := cloneFilterSet(p.trips[0].Filters)

	p.ToggleTripResort(0, "BBB") // add
	p.ToggleTripResort(0, "BBB") // remove -> back to seeded

	if !reflect.DeepEqual(p.trips[0].Filters, seeded) {
		t.Errorf("double toggle did not return to seeded state: got %+v, want %+v", p.trips[0].Filters, seeded)
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
