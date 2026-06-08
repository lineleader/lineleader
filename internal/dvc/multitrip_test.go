package dvc

import (
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

// --- Pure accounting helpers (free functions in planner.go) ---

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

// --- Multi-trip interaction scenarios (driven through the Planner) ---
//
// These exercise how trips share a single budget: a selection in one trip
// reduces every OTHER trip's effective budget, and removing a trip re-balances
// what's left. Single-trip recompute and the read API are covered in depth by
// planner_test.go; here we focus on the cross-trip interaction, asserting
// through the public Snapshot() projection.

// twoTripPlanner builds a Planner over the minimal chart with two trips on the
// same Jan 4–8 2026 window and the given budget.
func twoTripPlanner(budget string) *Planner {
	p := newTestPlanner(budget)
	p.AddTrip()
	return p
}

// TestMultiTrip_SelectionReducesOtherTripBudget verifies that selecting a stay
// in trip 0 lowers trip 1's EffectiveBudget by exactly the selected points,
// while trip 0's own effective budget is unaffected by its own selection.
func TestMultiTrip_SelectionReducesOtherTripBudget(t *testing.T) {
	p := twoTripPlanner("200")
	before := p.Snapshot()
	if len(before.Trips[0].Results) == 0 {
		t.Skip("no results for trip 0")
	}
	if before.Trips[1].EffectiveBudget != 200 {
		t.Fatalf("precondition: trip 1 EffectiveBudget = %d, want 200", before.Trips[1].EffectiveBudget)
	}

	// Select trip 0's most expensive result so the points are non-trivial.
	lastIdx := len(before.Trips[0].Results) - 1
	selPts := before.Trips[0].Results[lastIdx].Points
	if selPts == 0 {
		t.Skip("selected stay has 0 points; can't observe budget effect")
	}
	p.ToggleSelection(0, lastIdx)

	after := p.Snapshot()
	// Trip 0's own selection does not reduce its own effective budget.
	if after.Trips[0].EffectiveBudget != 200 {
		t.Errorf("trip 0 EffectiveBudget = %d, want 200 (own selection excluded)", after.Trips[0].EffectiveBudget)
	}
	// Trip 1's effective budget drops by the points trip 0 committed.
	if want := 200 - selPts; after.Trips[1].EffectiveBudget != want {
		t.Errorf("trip 1 EffectiveBudget = %d, want %d", after.Trips[1].EffectiveBudget, want)
	}
	// Global remaining drops too.
	if want := 200 - selPts; after.Remaining != want {
		t.Errorf("Remaining = %d, want %d", after.Remaining, want)
	}
}

// TestMultiTrip_DeselectRestoresOtherTripBudget verifies that deselecting in
// trip 0 returns trip 1's effective budget to the full global budget.
func TestMultiTrip_DeselectRestoresOtherTripBudget(t *testing.T) {
	p := twoTripPlanner("200")
	s := p.Snapshot()
	if len(s.Trips[0].Results) == 0 {
		t.Skip("no results for trip 0")
	}
	lastIdx := len(s.Trips[0].Results) - 1

	p.ToggleSelection(0, lastIdx) // select
	p.ToggleSelection(0, lastIdx) // deselect same row

	after := p.Snapshot()
	if after.Trips[0].Selected != nil {
		t.Error("trip 0 Selected not cleared after deselect")
	}
	if after.Trips[1].EffectiveBudget != 200 {
		t.Errorf("trip 1 EffectiveBudget = %d, want 200 (restored)", after.Trips[1].EffectiveBudget)
	}
	if after.Remaining != 200 {
		t.Errorf("Remaining = %d, want 200 (restored)", after.Remaining)
	}
}

// TestMultiTrip_RemoveTripRebalancesBudget verifies that removing a trip that
// holds a selection frees its points back to the remaining trips' budgets.
func TestMultiTrip_RemoveTripRebalancesBudget(t *testing.T) {
	p := twoTripPlanner("200")
	s := p.Snapshot()
	if len(s.Trips[0].Results) == 0 {
		t.Skip("no results for trip 0")
	}

	// Trip 0 commits points, squeezing trip 1's effective budget.
	lastIdx := len(s.Trips[0].Results) - 1
	selPts := s.Trips[0].Results[lastIdx].Points
	if selPts == 0 {
		t.Skip("selected stay has 0 points; can't observe rebalance")
	}
	p.ToggleSelection(0, lastIdx)

	squeezed := p.Snapshot()
	if want := 200 - selPts; squeezed.Trips[1].EffectiveBudget != want {
		t.Fatalf("precondition: trip 1 EffectiveBudget = %d, want %d", squeezed.Trips[1].EffectiveBudget, want)
	}

	// Removing trip 0 gives its points back: the surviving trip sees full budget.
	p.RemoveTrip(0)

	after := p.Snapshot()
	if len(after.Trips) != 1 {
		t.Fatalf("Trips = %d, want 1 after RemoveTrip", len(after.Trips))
	}
	if after.Trips[0].EffectiveBudget != 200 {
		t.Errorf("surviving trip EffectiveBudget = %d, want 200 (rebalanced)", after.Trips[0].EffectiveBudget)
	}
	if after.Remaining != 200 {
		t.Errorf("Remaining = %d, want 200 (rebalanced)", after.Remaining)
	}
}

// TestMultiTrip_InvalidBudgetMarksAllTrips verifies an unparseable global
// budget surfaces on the Snapshot and clears every trip's results.
func TestMultiTrip_InvalidBudgetMarksAllTrips(t *testing.T) {
	p := twoTripPlanner("200")
	p.SetBudget("not-a-number")

	s := p.Snapshot()
	if s.BudgetErr == "" {
		t.Error("expected Snapshot.BudgetErr for invalid budget, got empty")
	}
	for i, trip := range s.Trips {
		if len(trip.Results) != 0 {
			t.Errorf("trip %d: expected no results for invalid budget, got %d", i, len(trip.Results))
		}
	}
}
