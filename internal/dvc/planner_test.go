package dvc

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
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

// --- Plan operations ---

// newPlanPlanner builds a Planner with a plansPath under t.TempDir and two
// trips: trip 0 inherits, trip 1 overrides with specific filters.
func newPlanPlanner(t *testing.T) *Planner {
	t.Helper()
	p := NewPlanner(PlannerOptions{
		Charts:     []*ResortChart{minimalChart()},
		ConfigPath: filepath.Join(t.TempDir(), "config.json"),
		PlansPath:  filepath.Join(t.TempDir(), "plans.json"),
		Defaults: Defaults{
			From:      "2026-01-04",
			To:        "2026-01-08",
			Budget:    "200",
			MinNights: "1",
		},
	})
	p.trips = append(p.trips, Trip{
		Fields: [3]inputField{
			{label: "From", value: "2026-02-01"},
			{label: "To", value: "2026-02-05"},
			{label: "Min nights", value: "2"},
		},
		FilterMode: FilterModeOverride,
		Filters: FilterSet{
			ExcludeResorts:   []string{"AKV"},
			ExcludeRoomTypes: []string{"RESORT STUDIO"},
		},
	})
	p.recomputeAll()
	return p
}

func TestSavePlanLoadPlan_RoundTripsPerTripFilters(t *testing.T) {
	p := newPlanPlanner(t)
	// Set a global filter so the global round-trip is exercised too.
	p.global.ExcludeResorts = []string{"VERO"}

	if err := p.SavePlan("trip-plan"); err != nil {
		t.Fatalf("SavePlan: %v", err)
	}

	// Mutate live state so a successful Load is observable.
	p.SetBudget("999")
	p.SetTripField(0, 0, "2030-12-01")
	p.global.ExcludeResorts = nil
	p.trips[1].FilterMode = FilterModeInherit
	p.trips[1].Filters = FilterSet{}

	if !p.LoadPlan("trip-plan") {
		t.Fatal("LoadPlan returned false for an existing plan")
	}

	if p.budget != "200" {
		t.Errorf("budget = %q, want 200", p.budget)
	}
	if got := p.trips[0].Fields[0].value; got != "2026-01-04" {
		t.Errorf("trip 0 From = %q, want 2026-01-04", got)
	}
	if !slices.Contains(p.global.ExcludeResorts, "VERO") {
		t.Errorf("global ExcludeResorts = %v, want VERO", p.global.ExcludeResorts)
	}
	// Inherit trip restored as inherit, empty filters.
	if p.trips[0].FilterMode != FilterModeInherit {
		t.Errorf("trip 0 FilterMode = %q, want inherit", p.trips[0].FilterMode)
	}
	if len(p.trips[0].Filters.ExcludeResorts) != 0 || len(p.trips[0].Filters.ExcludeRoomTypes) != 0 {
		t.Errorf("trip 0 Filters = %+v, want empty", p.trips[0].Filters)
	}
	// Override trip restored with its specific filters.
	if p.trips[1].FilterMode != FilterModeOverride {
		t.Errorf("trip 1 FilterMode = %q, want override", p.trips[1].FilterMode)
	}
	wantFilters := FilterSet{
		ExcludeResorts:   []string{"AKV"},
		ExcludeRoomTypes: []string{"RESORT STUDIO"},
	}
	if !reflect.DeepEqual(p.trips[1].Filters, wantFilters) {
		t.Errorf("trip 1 Filters = %+v, want %+v", p.trips[1].Filters, wantFilters)
	}
	if p.LoadedPlanName() != "trip-plan" {
		t.Errorf("LoadedPlanName = %q, want trip-plan", p.LoadedPlanName())
	}
}

func TestSavePlan_UpsertsByName(t *testing.T) {
	p := newPlanPlanner(t)
	if err := p.SavePlan("p"); err != nil {
		t.Fatalf("SavePlan: %v", err)
	}
	p.SetBudget("321")
	if err := p.SavePlan("p"); err != nil {
		t.Fatalf("SavePlan overwrite: %v", err)
	}

	plans := p.Plans()
	if len(plans) != 1 {
		t.Fatalf("Plans() = %d, want 1 (no duplicate)", len(plans))
	}
	if plans[0].Budget != "321" {
		t.Errorf("plan Budget = %q, want 321 (overwritten)", plans[0].Budget)
	}
}

func TestDeletePlan_RemovesAndClearsLoadedName(t *testing.T) {
	p := newPlanPlanner(t)
	if err := p.SavePlan("a"); err != nil {
		t.Fatalf("SavePlan a: %v", err)
	}
	if err := p.SavePlan("b"); err != nil {
		t.Fatalf("SavePlan b: %v", err)
	}
	// b is the loaded plan (last saved). Delete a first: loaded name unchanged.
	if err := p.DeletePlan("a"); err != nil {
		t.Fatalf("DeletePlan a: %v", err)
	}
	if p.LoadedPlanName() != "b" {
		t.Errorf("after deleting a: LoadedPlanName = %q, want b", p.LoadedPlanName())
	}
	if len(p.Plans()) != 1 {
		t.Fatalf("Plans() = %d, want 1", len(p.Plans()))
	}
	// Delete b, the loaded plan: name cleared.
	if err := p.DeletePlan("b"); err != nil {
		t.Fatalf("DeletePlan b: %v", err)
	}
	if p.LoadedPlanName() != "" {
		t.Errorf("after deleting loaded plan: LoadedPlanName = %q, want empty", p.LoadedPlanName())
	}
	if len(p.Plans()) != 0 {
		t.Errorf("Plans() = %d, want 0", len(p.Plans()))
	}
}

func TestLoadPlan_UnknownNameLeavesStateUntouched(t *testing.T) {
	p := newPlanPlanner(t)
	budgetBefore := p.budget
	tripsBefore := len(p.trips)
	fromBefore := p.trips[0].Fields[0].value

	if p.LoadPlan("does-not-exist") {
		t.Fatal("LoadPlan returned true for an unknown name")
	}
	if p.budget != budgetBefore {
		t.Errorf("budget mutated: %q, want %q", p.budget, budgetBefore)
	}
	if len(p.trips) != tripsBefore {
		t.Errorf("trips count mutated: %d, want %d", len(p.trips), tripsBefore)
	}
	if p.trips[0].Fields[0].value != fromBefore {
		t.Errorf("trip 0 From mutated: %q, want %q", p.trips[0].Fields[0].value, fromBefore)
	}
	if p.LoadedPlanName() != "" {
		t.Errorf("LoadedPlanName = %q, want empty", p.LoadedPlanName())
	}
}

func TestSavePlan_PersistsAndReloadsPerTripFilters(t *testing.T) {
	plansPath := filepath.Join(t.TempDir(), "plans.json")
	p := NewPlanner(PlannerOptions{
		Charts:    []*ResortChart{minimalChart()},
		PlansPath: plansPath,
		Defaults: Defaults{
			From: "2026-01-04", To: "2026-01-08", Budget: "200", MinNights: "1",
		},
	})
	p.trips = append(p.trips, Trip{
		Fields: [3]inputField{
			{label: "From", value: "2026-02-01"},
			{label: "To", value: "2026-02-05"},
			{label: "Min nights", value: "2"},
		},
		FilterMode: FilterModeOverride,
		Filters: FilterSet{
			ExcludeResorts:   []string{"AKV"},
			ExcludeRoomTypes: []string{"RESORT STUDIO"},
		},
	})
	p.recomputeAll()

	if err := p.SavePlan("persisted"); err != nil {
		t.Fatalf("SavePlan: %v", err)
	}
	if _, err := os.Stat(plansPath); err != nil {
		t.Fatalf("plans file not written: %v", err)
	}

	plans, err := LoadPlans(plansPath)
	if err != nil {
		t.Fatalf("LoadPlans: %v", err)
	}
	if len(plans) != 1 {
		t.Fatalf("loaded %d plans, want 1", len(plans))
	}
	specs := plans[0].Trips
	if len(specs) != 2 {
		t.Fatalf("loaded %d trips, want 2", len(specs))
	}
	// Inherit trip must omit the filters key (nil pointer).
	if specs[0].Filters != nil {
		t.Errorf("inherit trip Filters = %+v, want nil", specs[0].Filters)
	}
	if specs[0].FilterMode != FilterModeInherit {
		t.Errorf("inherit trip FilterMode = %q, want inherit", specs[0].FilterMode)
	}
	// Override trip must round-trip its filters through JSON.
	if specs[1].FilterMode != FilterModeOverride {
		t.Errorf("override trip FilterMode = %q, want override", specs[1].FilterMode)
	}
	if specs[1].Filters == nil {
		t.Fatal("override trip Filters = nil, want a value")
	}
	want := FilterSet{
		ExcludeResorts:   []string{"AKV"},
		ExcludeRoomTypes: []string{"RESORT STUDIO"},
	}
	if !reflect.DeepEqual(*specs[1].Filters, want) {
		t.Errorf("override trip Filters = %+v, want %+v", *specs[1].Filters, want)
	}
}

func TestSavePlan_InheritTripSerializesWithoutFiltersKey(t *testing.T) {
	plansPath := filepath.Join(t.TempDir(), "plans.json")
	p := NewPlanner(PlannerOptions{
		Charts:    []*ResortChart{minimalChart()},
		PlansPath: plansPath,
		Defaults: Defaults{
			From: "2026-01-04", To: "2026-01-08", Budget: "200", MinNights: "1",
		},
	})
	if err := p.SavePlan("inherit-only"); err != nil {
		t.Fatalf("SavePlan: %v", err)
	}
	data, err := os.ReadFile(plansPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if strings.Contains(string(data), "\"filters\"") {
		t.Errorf("inherit trip serialized a filters key:\n%s", data)
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

// --- Snapshot + FilterOptions read API (lineleader-fpl.8) ---

// newMultiChartPlanner builds a Planner over twoResortCharts (resorts AAA/BBB,
// room types STUDIO/VILLA) with two inherit trips, exercising de-dup + sort.
func newMultiChartPlanner(t *testing.T, budget string) *Planner {
	t.Helper()
	p := NewPlanner(PlannerOptions{
		Charts:     twoResortCharts(),
		ConfigPath: filepath.Join(t.TempDir(), "config.json"),
		Defaults: Defaults{
			From:      "2026-01-04",
			To:        "2026-01-08",
			Budget:    budget,
			MinNights: "1",
		},
	})
	return p
}

func TestSnapshot_BudgetErrorAndFields(t *testing.T) {
	p := NewPlanner(PlannerOptions{
		Charts:    []*ResortChart{minimalChart()},
		PlansPath: filepath.Join(t.TempDir(), "plans.json"),
		Defaults: Defaults{
			From:      "2026-01-04",
			To:        "2026-01-08",
			Budget:    "not-a-number",
			MinNights: "1",
		},
	})
	if err := p.SavePlan("plan-a"); err != nil {
		t.Fatalf("SavePlan: %v", err)
	}

	s := p.Snapshot()
	if s.Budget != "not-a-number" {
		t.Errorf("Budget = %q, want %q", s.Budget, "not-a-number")
	}
	if s.BudgetErr != "invalid Budget" {
		t.Errorf("BudgetErr = %q, want %q", s.BudgetErr, "invalid Budget")
	}
	if s.LoadedPlanName != "plan-a" {
		t.Errorf("LoadedPlanName = %q, want %q", s.LoadedPlanName, "plan-a")
	}

	p.SetBudget("200")
	s = p.Snapshot()
	if s.BudgetErr != "" {
		t.Errorf("BudgetErr = %q, want empty for valid budget", s.BudgetErr)
	}
}

func TestSnapshot_RemainingAndEffectiveBudget(t *testing.T) {
	p := newTestPlanner("200")
	p.AddTrip()
	// Select a stay in trip 0 so trip 1's effective budget is reduced.
	p.ToggleSelection(0, 0)
	selPts := p.trips[0].Selected.Points
	if selPts == 0 {
		t.Fatal("precondition: expected a non-zero selected points")
	}

	s := p.Snapshot()
	if len(s.Trips) != 2 {
		t.Fatalf("Trips = %d, want 2", len(s.Trips))
	}
	if want := 200 - selPts; s.Remaining != want {
		t.Errorf("Remaining = %d, want %d", s.Remaining, want)
	}
	// Trip 0's own selection does not reduce its own effective budget.
	if s.Trips[0].EffectiveBudget != 200 {
		t.Errorf("Trips[0].EffectiveBudget = %d, want 200", s.Trips[0].EffectiveBudget)
	}
	// Trip 1's effective budget excludes trip 0's selection.
	if want := 200 - selPts; s.Trips[1].EffectiveBudget != want {
		t.Errorf("Trips[1].EffectiveBudget = %d, want %d", s.Trips[1].EffectiveBudget, want)
	}
	if s.Trips[0].Selected == nil {
		t.Error("Trips[0].Selected = nil, want non-nil")
	}
}

func TestSnapshot_SpecFilterModeAndEffectiveFilters(t *testing.T) {
	p := newPerTripPlanner(t, Config{})
	p.ToggleGlobalResort("GLOB") // global exclusion (inherit trip sees it)
	p.ToggleTripResort(1, "TRIP") // trip 1 -> override seeded from global, plus TRIP

	s := p.Snapshot()

	// Trip 0 inherits: Mode empty, Spec.Filters nil, EffectiveFilters == global.
	if s.Trips[0].Spec.FilterMode != FilterModeInherit {
		t.Errorf("Trips[0].FilterMode = %q, want inherit", s.Trips[0].Spec.FilterMode)
	}
	if s.Trips[0].Spec.Filters != nil {
		t.Errorf("Trips[0].Spec.Filters = %+v, want nil", s.Trips[0].Spec.Filters)
	}
	if !slices.Contains(s.Trips[0].EffectiveFilters.ExcludeResorts, "GLOB") {
		t.Errorf("Trips[0].EffectiveFilters missing global GLOB: %+v", s.Trips[0].EffectiveFilters)
	}

	// Trip 1 override: Mode override, Spec.Filters non-nil with GLOB (seed) + TRIP.
	if s.Trips[1].Spec.FilterMode != FilterModeOverride {
		t.Errorf("Trips[1].FilterMode = %q, want override", s.Trips[1].Spec.FilterMode)
	}
	if s.Trips[1].Spec.Filters == nil {
		t.Fatal("Trips[1].Spec.Filters = nil, want non-nil")
	}
	if !slices.Contains(s.Trips[1].EffectiveFilters.ExcludeResorts, "TRIP") {
		t.Errorf("Trips[1].EffectiveFilters missing TRIP: %+v", s.Trips[1].EffectiveFilters)
	}
}

func TestSnapshot_Detached(t *testing.T) {
	p := newTestPlanner("200")
	p.AddTrip()

	s := p.Snapshot()
	if len(s.Trips) != 2 || len(s.Trips[0].Results) == 0 {
		t.Fatalf("precondition: trips=%d results=%d", len(s.Trips), len(s.Trips[0].Results))
	}
	internalResults := len(p.trips[0].Results)

	// Mutate the returned Trips slice and a returned Results slice.
	s.Trips = s.Trips[:1]
	s.Trips[0].Results[0] = StayResult{Resort: "MUTATED"}
	s.Trips[0].Spec.Filters = &FilterSet{ExcludeResorts: []string{"MUT"}}

	// A later Planner op / snapshot must be unaffected.
	p.SetBudget("200")
	s2 := p.Snapshot()
	if len(s2.Trips) != 2 {
		t.Errorf("internal Trips len affected: got %d, want 2", len(s2.Trips))
	}
	if len(p.trips[0].Results) != internalResults {
		t.Errorf("internal Results len changed: got %d, want %d", len(p.trips[0].Results), internalResults)
	}
	if p.trips[0].Results[0].Resort == "MUTATED" {
		t.Error("mutating returned Results changed Planner internal state")
	}
	if p.trips[0].FilterMode != FilterModeInherit {
		t.Error("mutating returned Spec changed Planner internal trip filters")
	}
}

func TestFilterOptions_GlobalSortedDedupedAndEnabled(t *testing.T) {
	p := newMultiChartPlanner(t, "200")
	p.ToggleGlobalResort("AAA")
	p.ToggleGlobalRoomType("STUDIO")

	v := p.FilterOptions(-1)
	if v.TripIndex != -1 {
		t.Errorf("TripIndex = %d, want -1", v.TripIndex)
	}
	if v.Mode != "" {
		t.Errorf("Mode = %q, want empty for global", v.Mode)
	}

	gotCodes := make([]string, len(v.Resorts))
	for i, r := range v.Resorts {
		gotCodes[i] = r.Code
	}
	wantCodes := []string{"AAA", "BBB"} // sorted, de-duped
	if !slices.Equal(gotCodes, wantCodes) {
		t.Errorf("resort codes = %v, want %v", gotCodes, wantCodes)
	}

	gotRooms := make([]string, len(v.RoomTypes))
	for i, r := range v.RoomTypes {
		gotRooms[i] = r.Name
	}
	wantRooms := []string{"STUDIO", "VILLA"} // sorted, de-duped
	if !slices.Equal(gotRooms, wantRooms) {
		t.Errorf("room types = %v, want %v", gotRooms, wantRooms)
	}

	// Enabled reflects global exclusions.
	for _, r := range v.Resorts {
		want := r.Code != "AAA"
		if r.Enabled != want {
			t.Errorf("resort %s Enabled = %v, want %v", r.Code, r.Enabled, want)
		}
	}
	for _, r := range v.RoomTypes {
		want := r.Name != "STUDIO"
		if r.Enabled != want {
			t.Errorf("room %s Enabled = %v, want %v", r.Name, r.Enabled, want)
		}
	}
}

func TestFilterOptions_InheritTripMatchesGlobal(t *testing.T) {
	p := newMultiChartPlanner(t, "200")
	p.AddTrip()
	p.ToggleGlobalResort("AAA")

	g := p.FilterOptions(-1)
	trip := p.FilterOptions(1) // inherit
	if trip.Mode != FilterModeInherit {
		t.Errorf("inherit trip Mode = %q, want inherit", trip.Mode)
	}
	if trip.TripIndex != 1 {
		t.Errorf("TripIndex = %d, want 1", trip.TripIndex)
	}
	for i := range g.Resorts {
		if g.Resorts[i].Enabled != trip.Resorts[i].Enabled {
			t.Errorf("resort %s: global Enabled=%v, inherit trip Enabled=%v",
				g.Resorts[i].Code, g.Resorts[i].Enabled, trip.Resorts[i].Enabled)
		}
	}
}

func TestFilterOptions_OverrideTripReflectsOwnSet(t *testing.T) {
	p := newMultiChartPlanner(t, "200")
	p.AddTrip()
	p.ToggleGlobalResort("AAA")  // global excludes AAA
	p.ToggleTripResort(1, "BBB") // trip 1 override: seeded with AAA, plus BBB

	v := p.FilterOptions(1)
	if v.Mode != FilterModeOverride {
		t.Errorf("Mode = %q, want override", v.Mode)
	}
	byCode := map[string]bool{}
	for _, r := range v.Resorts {
		byCode[r.Code] = r.Enabled
	}
	if byCode["AAA"] {
		t.Error("AAA should be disabled (seeded from global) in override trip")
	}
	if byCode["BBB"] {
		t.Error("BBB should be disabled (toggled) in override trip")
	}
}

func TestFilterOptions_OutOfRangeTreatedAsGlobal(t *testing.T) {
	p := newMultiChartPlanner(t, "200")
	p.ToggleGlobalResort("AAA")

	// Out-of-range tripIdx (>= len) is treated as global: TripIndex echoes -1.
	v := p.FilterOptions(99)
	g := p.FilterOptions(-1)
	if v.TripIndex != -1 {
		t.Errorf("out-of-range TripIndex = %d, want -1 (treated as global)", v.TripIndex)
	}
	if v.Mode != "" {
		t.Errorf("out-of-range Mode = %q, want empty", v.Mode)
	}
	if len(v.Resorts) != len(g.Resorts) {
		t.Fatalf("resort count = %d, want %d", len(v.Resorts), len(g.Resorts))
	}
	for i := range g.Resorts {
		if v.Resorts[i] != g.Resorts[i] {
			t.Errorf("resort %d = %+v, want %+v", i, v.Resorts[i], g.Resorts[i])
		}
	}
}
