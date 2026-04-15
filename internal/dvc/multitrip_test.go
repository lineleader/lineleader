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
