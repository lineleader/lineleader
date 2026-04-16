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

// --- Group 2: recomputeTrip ---

func newTestTrip(from, to, minNights string) Trip {
	t := Trip{}
	t.Fields[0] = inputField{label: "From", value: from}
	t.Fields[1] = inputField{label: "To", value: to}
	t.Fields[2] = inputField{label: "Min nights", value: minNights}
	return t
}

func TestRecomputeTrip_ValidParams(t *testing.T) {
	chart := minimalChart()
	trip := newTestTrip("2026-01-04", "2026-01-08", "1")
	trip = recomputeTrip([]*ResortChart{chart}, trip, 100, Config{})
	if trip.Err != "" {
		t.Fatalf("unexpected error: %s", trip.Err)
	}
	if len(trip.Results) == 0 {
		t.Error("expected results, got none")
	}
}

func TestRecomputeTrip_InvalidDate(t *testing.T) {
	chart := minimalChart()
	trip := newTestTrip("not-a-date", "2026-01-08", "1")
	before := []StayResult{{Resort: "prev"}}
	trip.Results = before
	trip = recomputeTrip([]*ResortChart{chart}, trip, 100, Config{})
	if trip.Err == "" {
		t.Error("expected error for invalid date")
	}
	// Results must not be cleared on error (preserve last good set).
	if len(trip.Results) != 1 {
		t.Errorf("results changed on invalid input: got %d, want 1", len(trip.Results))
	}
}

func TestRecomputeTrip_BudgetPropagated(t *testing.T) {
	chart := minimalChart()
	// Col 0 costs 10/night weekday. 4 nights = 40 pts.
	// With budget 45 we get results; with budget 9 we get none.
	trip := newTestTrip("2026-01-04", "2026-01-08", "4")
	tripHigh := recomputeTrip([]*ResortChart{chart}, trip, 45, Config{})
	tripLow := recomputeTrip([]*ResortChart{chart}, trip, 9, Config{})
	if len(tripHigh.Results) == 0 {
		t.Error("expected results with budget 45")
	}
	if len(tripLow.Results) != 0 {
		t.Errorf("expected 0 results with budget 9, got %d", len(tripLow.Results))
	}
}

func TestRecomputeTrip_OffsetClamped(t *testing.T) {
	chart := minimalChart()
	trip := newTestTrip("2026-01-04", "2026-01-08", "1")
	trip = recomputeTrip([]*ResortChart{chart}, trip, 200, Config{})
	if len(trip.Results) == 0 {
		t.Skip("no results, skipping offset clamp test")
	}
	trip.Offset = len(trip.Results) - 1
	// Tighten budget to zero results; offset should be clamped to 0.
	trip = recomputeTrip([]*ResortChart{chart}, trip, 1, Config{})
	if len(trip.Results) > 0 && trip.Offset >= len(trip.Results) {
		t.Errorf("offset %d not clamped; results len = %d", trip.Offset, len(trip.Results))
	}
	if len(trip.Results) == 0 && trip.Offset != 0 {
		t.Errorf("offset %d should be 0 when results are empty", trip.Offset)
	}
}

// --- Group 3: recomputeAll ---

func newTwoTripModel() tuiModel {
	chart := minimalChart()
	m := newTUIModel([]*ResortChart{chart})
	m.budgetField.value = "200"
	// Trip 0: Jan 4–8
	m.trips[0].Fields[0].value = "2026-01-04"
	m.trips[0].Fields[1].value = "2026-01-08"
	m.trips[0].Fields[2].value = "1"
	// Add trip 1: same dates.
	trip1 := Trip{
		Fields: [3]inputField{
			{label: "From", value: "2026-01-04"},
			{label: "To", value: "2026-01-08"},
			{label: "Min nights", value: "1"},
		},
	}
	m.trips = append(m.trips, trip1)
	return m.recomputeAll()
}

func TestRecomputeAll_TwoTrips_ShareBudget(t *testing.T) {
	m := newTwoTripModel()
	if len(m.trips[0].Results) == 0 {
		t.Skip("no results for trip 0")
	}
	before1 := len(m.trips[1].Results)

	// Select the most expensive result for trip 0.
	last := m.trips[0].Results[len(m.trips[0].Results)-1]
	m.trips[0].Selected = &last
	m = m.recomputeAll()

	// Trip 1 should have fewer or equal results after trip 0 commits points.
	if last.Points > 0 && len(m.trips[1].Results) > before1 {
		t.Errorf("trip 1 results grew after trip 0 selected %d pts: before=%d after=%d",
			last.Points, before1, len(m.trips[1].Results))
	}
}

func TestRecomputeAll_Deselect_RestoresBudget(t *testing.T) {
	m := newTwoTripModel()
	if len(m.trips[0].Results) == 0 {
		t.Skip("no results")
	}
	before1 := len(m.trips[1].Results)

	// Select then deselect.
	last := m.trips[0].Results[len(m.trips[0].Results)-1]
	m.trips[0].Selected = &last
	m = m.recomputeAll()
	m.trips[0].Selected = nil
	m = m.recomputeAll()

	if len(m.trips[1].Results) != before1 {
		t.Errorf("after deselect, trip 1 results = %d, want %d (restored)", len(m.trips[1].Results), before1)
	}
}

func TestRecomputeAll_InvalidBudgetField(t *testing.T) {
	m := newTwoTripModel()
	m.budgetField.value = "not-a-number"
	m = m.recomputeAll()
	for i, trip := range m.trips {
		if trip.Err == "" {
			t.Errorf("trip %d: expected Err for invalid budget, got empty", i)
		}
		if len(trip.Results) != 0 {
			t.Errorf("trip %d: expected nil results for invalid budget, got %d", i, len(trip.Results))
		}
	}
}
