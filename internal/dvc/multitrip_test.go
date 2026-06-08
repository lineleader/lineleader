package dvc

import (
	"strings"
	"testing"
	"time"
)

// makeTrip returns a Trip with the given selection points (0 = no selection).
func makeTrip(selectedPts int) Trip {
	t := Trip{}
	if selectedPts > 0 {
		stay := StayResult{Points: selectedPts}
		t.Selected = &stay
	}
	return t
}

// --- Group 1: pure accounting ---

func TestSelectedPoints_NoSelections(t *testing.T) {
	trips := []Trip{makeTrip(0), makeTrip(0)}
	if got := SelectedPoints(trips); got != 0 {
		t.Errorf("SelectedPoints = %d, want 0", got)
	}
}

func TestSelectedPoints_OneSelection(t *testing.T) {
	trips := []Trip{makeTrip(40), makeTrip(0)}
	if got := SelectedPoints(trips); got != 40 {
		t.Errorf("SelectedPoints = %d, want 40", got)
	}
}

func TestSelectedPoints_MultipleSelections(t *testing.T) {
	trips := []Trip{makeTrip(40), makeTrip(60)}
	if got := SelectedPoints(trips); got != 100 {
		t.Errorf("SelectedPoints = %d, want 100", got)
	}
}

func TestRemainingBudget_NoSelections(t *testing.T) {
	trips := []Trip{makeTrip(0), makeTrip(0)}
	if got := RemainingBudget(200, trips); got != 200 {
		t.Errorf("RemainingBudget = %d, want 200", got)
	}
}

func TestRemainingBudget_SomeSelected(t *testing.T) {
	trips := []Trip{makeTrip(40), makeTrip(0)}
	if got := RemainingBudget(200, trips); got != 160 {
		t.Errorf("RemainingBudget = %d, want 160", got)
	}
}

func TestBudgetForTrip_ExcludesOwnSelection(t *testing.T) {
	// Trip 0 selected 40, trip 1 selected 60.
	// BudgetForTrip(200, trips, 0) should exclude trip 1's 60 → 140.
	trips := []Trip{makeTrip(40), makeTrip(60)}
	if got := BudgetForTrip(200, trips, 0); got != 140 {
		t.Errorf("BudgetForTrip(200, trips, 0) = %d, want 140", got)
	}
}

func TestBudgetForTrip_OnlyOtherTrips(t *testing.T) {
	// Trip 0 has no selection; trip 1 selected 60.
	// BudgetForTrip(200, trips, 0) = 200 - 60 = 140.
	trips := []Trip{makeTrip(0), makeTrip(60)}
	if got := BudgetForTrip(200, trips, 0); got != 140 {
		t.Errorf("BudgetForTrip(200, trips, 0) = %d, want 140", got)
	}
	// BudgetForTrip(200, trips, 1) = 200 - 0 = 200 (trip 0 has no selection).
	if got := BudgetForTrip(200, trips, 1); got != 200 {
		t.Errorf("BudgetForTrip(200, trips, 1) = %d, want 200", got)
	}
}

func TestStayEquals_SameFields(t *testing.T) {
	checkIn, _ := time.Parse("2006-01-02", "2026-01-04")
	checkOut, _ := time.Parse("2006-01-02", "2026-01-08")
	a := StayResult{Resort: "VGF", RoomType: "STUDIO", View: "R", CheckIn: checkIn, CheckOut: checkOut}
	b := a
	if !stayEquals(a, b) {
		t.Error("stayEquals returned false for identical stays")
	}
}

func TestStayEquals_DifferentFields(t *testing.T) {
	checkIn, _ := time.Parse("2006-01-02", "2026-01-04")
	checkOut, _ := time.Parse("2006-01-02", "2026-01-08")
	a := StayResult{Resort: "VGF", RoomType: "STUDIO", View: "R", CheckIn: checkIn, CheckOut: checkOut}
	b := a
	b.View = "P"
	if stayEquals(a, b) {
		t.Error("stayEquals returned true for stays with different View")
	}
}

// --- Group 2 & 3: recompute via the Planner ---
//
// These behaviors (single-trip recompute, multi-trip shared budget) now live on
// *Planner; planner_test.go covers them in depth. The view-layer checks below
// drive a *Planner through the tuiModel and read back its Snapshot. A full
// rewrite of this file against the Planner is fpl.14; these are minimal edits to
// keep the suite green.

func newTwoTripModel() tuiModel {
	m := NewTUIModel(PlannerOptions{
		Charts: []*ResortChart{minimalChart()},
		Defaults: Defaults{
			From:      "2026-01-04",
			To:        "2026-01-08",
			Budget:    "200",
			MinNights: "1",
		},
	})
	// Add a second trip (clones trip 0's dates, min-nights reset to 1).
	m.planner.AddTrip()
	m.refresh()
	return m
}

func TestRecomputeAll_TwoTrips_ShareBudget(t *testing.T) {
	m := newTwoTripModel()
	if len(m.snap.Trips[0].Results) == 0 {
		t.Skip("no results for trip 0")
	}
	before1 := len(m.snap.Trips[1].Results)

	// Select the most expensive result for trip 0.
	lastIdx := len(m.snap.Trips[0].Results) - 1
	last := m.snap.Trips[0].Results[lastIdx]
	m.planner.ToggleSelection(0, lastIdx)
	m.refresh()

	// Trip 1 should have fewer or equal results after trip 0 commits points.
	if last.Points > 0 && len(m.snap.Trips[1].Results) > before1 {
		t.Errorf("trip 1 results grew after trip 0 selected %d pts: before=%d after=%d",
			last.Points, before1, len(m.snap.Trips[1].Results))
	}
}

func TestRecomputeAll_Deselect_RestoresBudget(t *testing.T) {
	m := newTwoTripModel()
	if len(m.snap.Trips[0].Results) == 0 {
		t.Skip("no results")
	}
	before1 := len(m.snap.Trips[1].Results)

	// Select then deselect the last result.
	lastIdx := len(m.snap.Trips[0].Results) - 1
	m.planner.ToggleSelection(0, lastIdx)
	m.refresh()
	m.planner.ToggleSelection(0, lastIdx)
	m.refresh()

	if len(m.snap.Trips[1].Results) != before1 {
		t.Errorf("after deselect, trip 1 results = %d, want %d (restored)", len(m.snap.Trips[1].Results), before1)
	}
}

func TestRecomputeAll_InvalidBudgetField(t *testing.T) {
	m := newTwoTripModel()
	m.planner.SetBudget("not-a-number")
	m.refresh()
	if m.snap.BudgetErr == "" {
		t.Error("expected Snapshot.BudgetErr for invalid budget, got empty")
	}
	for i, trip := range m.snap.Trips {
		if len(trip.Results) != 0 {
			t.Errorf("trip %d: expected no results for invalid budget, got %d", i, len(trip.Results))
		}
	}
}

// --- Group 5: View smoke tests ---

func TestTUIView_ShowsAllTrips(t *testing.T) {
	m := newTwoTripModel()
	m.height = 40
	m.width = 100
	v := m.View()
	out := v.Content
	if !strings.Contains(out, "TRIP 1") {
		t.Error("View missing 'TRIP 1'")
	}
	if !strings.Contains(out, "TRIP 2") {
		t.Error("View missing 'TRIP 2'")
	}
}

func TestTUIView_ShowsRemainingBudget(t *testing.T) {
	m := newTwoTripModel()
	m.height = 40
	m.width = 100
	v := m.View()
	out := v.Content
	if !strings.Contains(out, "Remaining:") {
		t.Error("View missing 'Remaining:' counter")
	}
}

func TestTUIView_ShowsSelectedMark(t *testing.T) {
	m := newTwoTripModel()
	m.height = 40
	m.width = 100
	if len(m.snap.Trips[0].Results) == 0 {
		t.Skip("no results to select")
	}
	m.planner.ToggleSelection(0, 0)
	m.refresh()
	v := m.View()
	out := v.Content
	if !strings.Contains(out, "✓") {
		t.Error("View missing '✓' mark for selected stay")
	}
}
