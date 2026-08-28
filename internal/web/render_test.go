package web

import (
	"testing"
	"time"

	"github.com/lineleader/lineleader/internal/dvc"
	"github.com/lineleader/lineleader/internal/ledger"
)

// A global FilterOptionsView projects a non-trip scope when the caller passes
// filterScope{}.
func TestToFiltersView_GlobalScope(t *testing.T) {
	fv := toFiltersView(dvc.FilterOptionsView{
		Resorts:   []dvc.ResortOption{{Code: "TST", Name: "Test Resort", Enabled: true}},
		RoomTypes: []dvc.RoomTypeOption{{Name: "Studio", Enabled: false}},
	}, filterScope{})
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

// A trip-scoped FilterOptionsView, paired with a caller-constructed
// filterScope, projects the trip scope carrying TripIndex and Mode.
func TestToFiltersView_TripScope(t *testing.T) {
	fv := toFiltersView(dvc.FilterOptionsView{
		Resorts:   []dvc.ResortOption{{Code: "TST", Name: "Test Resort", Enabled: false}},
		RoomTypes: []dvc.RoomTypeOption{{Name: "Studio", Enabled: true}},
	}, filterScope{IsTrip: true, TripIndex: 1, Mode: dvc.FilterModeOverride})

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

// --- buildTripView: pure, no database, must never t.Skip ---

func tripFixture() ledger.Trip {
	return ledger.Trip{
		ID:        1,
		Name:      "Summer trip",
		StartDate: time.Date(2026, 6, 4, 0, 0, 0, 0, time.UTC),
		EndDate:   time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC),
		MinNights: 3,
	}
}

func entryID(id int64) *int64 { return &id }

func TestBuildTripView_DerivesStatusFromEntryID(t *testing.T) {
	cases := []struct {
		name             string
		stays            []ledger.TripStay
		wantBooked       bool
		wantPartlyBooked bool
	}{
		{
			name:  "no stays",
			stays: nil,
		},
		{
			name: "all unbooked",
			stays: []ledger.TripStay{
				{Points: 10, EntryID: nil},
				{Points: 20, EntryID: nil},
			},
		},
		{
			name: "all booked",
			stays: []ledger.TripStay{
				{Points: 10, EntryID: entryID(1)},
				{Points: 20, EntryID: entryID(2)},
			},
			wantBooked: true,
		},
		{
			name: "mixed",
			stays: []ledger.TripStay{
				{Points: 10, EntryID: entryID(1)},
				{Points: 20, EntryID: nil},
			},
			wantPartlyBooked: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tv := buildTripView(tripFixture(), c.stays, ledger.TripBudget{}, nil, time.January, ledger.CostBasis{}, false)
			if tv.Booked != c.wantBooked {
				t.Errorf("Booked = %v, want %v", tv.Booked, c.wantBooked)
			}
			if tv.PartlyBooked != c.wantPartlyBooked {
				t.Errorf("PartlyBooked = %v, want %v", tv.PartlyBooked, c.wantPartlyBooked)
			}
		})
	}
}

func TestBuildTripView_BudgetLabelsAreSigned(t *testing.T) {
	b := ledger.TripBudget{UseYear: 2026, Current: 270, Banked: -60, Borrowable: 270, Total: 480}

	tv := buildTripView(tripFixture(), nil, b, nil, time.January, ledger.CostBasis{}, false)

	if tv.Budget.CurrentLabel != "+270" {
		t.Errorf("CurrentLabel = %q, want %q", tv.Budget.CurrentLabel, "+270")
	}
	if tv.Budget.BankedLabel != "-60" {
		t.Errorf("BankedLabel = %q, want %q", tv.Budget.BankedLabel, "-60")
	}
	if tv.Budget.BorrowableLabel != "+270" {
		t.Errorf("BorrowableLabel = %q, want %q", tv.Budget.BorrowableLabel, "+270")
	}
	if tv.Budget.Total != 480 {
		t.Errorf("Total = %d, want 480", tv.Budget.Total)
	}
}

func TestBuildTripView_SumsStayPoints(t *testing.T) {
	stays := []ledger.TripStay{
		{Points: 40, EntryID: entryID(1)}, // booked
		{Points: 25, EntryID: nil},        // unbooked
		{Points: 15, EntryID: nil},        // unbooked
	}

	tv := buildTripView(tripFixture(), stays, ledger.TripBudget{}, nil, time.January, ledger.CostBasis{}, false)

	if tv.StaysPoints != 80 {
		t.Errorf("StaysPoints = %d, want 80 (summed over booked and unbooked alike)", tv.StaysPoints)
	}
}

func TestBuildTripView_PricesResultsWhenShowCosts(t *testing.T) {
	basis := ledger.NewCostBasis(
		[]ledger.Contract{{
			ID: 1, AnnualPoints: 100, TermYears: 10, PurchasePrice: 100_000_00,
			UseYearMonth: time.January,
		}},
		[]ledger.DuesRate{{UseYear: 2026, Rate: 8_223_500}},
	)
	if !basis.Known() {
		t.Fatal("precondition: basis should be Known()")
	}

	results := []dvc.StayResult{
		{Resort: "R1", RoomType: "STUDIO", CheckIn: time.Date(2026, 6, 4, 0, 0, 0, 0, time.UTC), CheckOut: time.Date(2026, 6, 6, 0, 0, 0, 0, time.UTC), Nights: 2, Points: 20},
		{Resort: "R2", RoomType: "VILLA", CheckIn: time.Date(2026, 6, 4, 0, 0, 0, 0, time.UTC), CheckOut: time.Date(2026, 6, 6, 0, 0, 0, 0, time.UTC), Nights: 2, Points: 40},
	}

	tvOn := buildTripView(tripFixture(), nil, ledger.TripBudget{}, results, time.January, basis, true)
	if len(tvOn.Results) != 2 {
		t.Fatalf("len(Results) = %d, want 2", len(tvOn.Results))
	}
	for _, r := range tvOn.Results {
		if r.CostLabel == "" {
			t.Errorf("result row CostLabel empty with showCosts=true, want a priced $ label for %+v", r)
		}
	}

	tvOff := buildTripView(tripFixture(), nil, ledger.TripBudget{}, results, time.January, basis, false)
	for _, r := range tvOff.Results {
		if r.CostLabel != "" {
			t.Errorf("result row CostLabel = %q, want empty with showCosts=false", r.CostLabel)
		}
	}
}

func TestBuildTripView_NoStaysNoResults(t *testing.T) {
	tv := buildTripView(ledger.Trip{}, nil, ledger.TripBudget{}, nil, time.January, ledger.CostBasis{}, false)

	if tv.Booked || tv.PartlyBooked {
		t.Errorf("zero-value trip should be neither booked nor partly booked: %+v", tv)
	}
	if len(tv.Stays) != 0 {
		t.Errorf("len(Stays) = %d, want 0", len(tv.Stays))
	}
	if len(tv.Results) != 0 {
		t.Errorf("len(Results) = %d, want 0", len(tv.Results))
	}
	if tv.StaysPoints != 0 {
		t.Errorf("StaysPoints = %d, want 0", tv.StaysPoints)
	}
}
