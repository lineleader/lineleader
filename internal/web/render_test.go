package web

import (
	"strings"
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

// intPtr is a tiny helper so table cases can express a *int literal inline.
func intPtr(v int) *int { return &v }

// TestBuildTripView_SpansUseYears is table-driven over (window, use-year
// start month) pairs. The first two rows share the April use year the doc
// comment describes. The third row deliberately uses April too, NOT
// January: with a January start month ledger.UseYearForDate collapses to
// the bare calendar year for every date (the ">= startMonth" branch is
// always taken), so Dec 28 2026 and Jan 3 2027 land in UY2026 and UY2027
// respectively — genuinely different use years, and the naive
// StartDate.Year()-vs-EndDate.Year() mutation (c) would AGREE with that
// correct answer instead of being caught by it. Under the April use year
// shared by the rest of this table, both dates fall in UY2026 (Dec is
// still before the following April's boundary), so this row is exactly
// the case that catches a naive calendar-year comparison: it says "spans"
// (2026 != 2027) while the correct use-year-aware comparison says "does
// not span".
func TestBuildTripView_SpansUseYears(t *testing.T) {
	cases := []struct {
		name           string
		start, end     time.Time
		month          time.Month
		wantSpans      bool
		wantNoteSubstr []string
	}{
		{
			name:      "spans April use year",
			start:     time.Date(2026, 3, 20, 0, 0, 0, 0, time.UTC),
			end:       time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC),
			month:     time.April,
			wantSpans: true,
			wantNoteSubstr: []string{
				"use years 2025 → 2026",
				"UY2025",
				"2026-04-01",
				"UY2026",
			},
		},
		{
			name:      "within one use year",
			start:     time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
			end:       time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC),
			month:     time.April,
			wantSpans: false,
		},
		{
			name:      "Dec-to-Jan window does not cross the April boundary",
			start:     time.Date(2026, 12, 28, 0, 0, 0, 0, time.UTC),
			end:       time.Date(2027, 1, 3, 0, 0, 0, 0, time.UTC),
			month:     time.April,
			wantSpans: false,
		},
		{
			// The SAME window on a JANUARY use year does span. A January use
			// year makes every date's use year its calendar year, so Jan 3
			// 2027 is UY2027 while Dec 28 2026 is UY2026.
			//
			// This is the distinction the design doc's edge-case table is
			// easy to misread: "Dec 28 2026 → Jan 3 2027, January UY → UY2026,
			// wholly" is about where a single STAY is charged (by check-in,
			// never split), NOT about whether a trip's WINDOW straddles a
			// boundary. The window does, and must warn.
			name:      "Dec-to-Jan window crosses a January boundary",
			start:     time.Date(2026, 12, 28, 0, 0, 0, 0, time.UTC),
			end:       time.Date(2027, 1, 3, 0, 0, 0, 0, time.UTC),
			month:     time.January,
			wantSpans: true,
			wantNoteSubstr: []string{
				"use years 2026 → 2027",
				"UY2026",
				"2027-01-01",
				"UY2027",
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tr := tripFixture()
			tr.StartDate = c.start
			tr.EndDate = c.end

			tv := buildTripView(tr, nil, ledger.TripBudget{}, nil, c.month, ledger.CostBasis{}, false)

			if tv.SpansUseYears != c.wantSpans {
				t.Errorf("SpansUseYears = %v, want %v (note = %q)", tv.SpansUseYears, c.wantSpans, tv.SpanNote)
			}
			if !c.wantSpans && tv.SpanNote != "" {
				t.Errorf("SpanNote = %q, want empty when not spanning", tv.SpanNote)
			}
			for _, want := range c.wantNoteSubstr {
				if !strings.Contains(tv.SpanNote, want) {
					t.Errorf("SpanNote = %q, want substring %q", tv.SpanNote, want)
				}
			}
		})
	}
}

func TestBuildTripView_EffectiveBudgetHonoursOverride(t *testing.T) {
	b := ledger.TripBudget{Total: 480}

	tr := tripFixture()
	tvNil := buildTripView(tr, nil, b, nil, time.January, ledger.CostBasis{}, false)
	if tvNil.EffectiveBudget != 480 {
		t.Errorf("EffectiveBudget (nil override) = %d, want 480", tvNil.EffectiveBudget)
	}
	if tvNil.BudgetOverridden {
		t.Errorf("BudgetOverridden (nil override) = true, want false")
	}
	if tvNil.Budget.Overridden {
		t.Errorf("Budget.Overridden (nil override) = true, want false")
	}
	if tvNil.Budget.ComputedTotal != 480 {
		t.Errorf("Budget.ComputedTotal (nil override) = %d, want 480", tvNil.Budget.ComputedTotal)
	}

	tr.BudgetOverride = intPtr(200)
	tvSet := buildTripView(tr, nil, b, nil, time.January, ledger.CostBasis{}, false)
	if tvSet.EffectiveBudget != 200 {
		t.Errorf("EffectiveBudget (override 200) = %d, want 200", tvSet.EffectiveBudget)
	}
	if !tvSet.BudgetOverridden {
		t.Errorf("BudgetOverridden (override 200) = false, want true")
	}
	if !tvSet.Budget.Overridden {
		t.Errorf("Budget.Overridden (override 200) = false, want true")
	}
	if tvSet.Budget.ComputedTotal != 480 {
		t.Errorf("Budget.ComputedTotal (override 200) = %d, want 480 (still the computed total)", tvSet.Budget.ComputedTotal)
	}
}

// TestEffectiveBudget_ZeroOverrideIsRespected pins the classic *int
// nil-vs-zero bug: a BudgetOverride pointing at 0 must yield 0, not fall
// through to the computed total.
func TestEffectiveBudget_ZeroOverrideIsRespected(t *testing.T) {
	tr := tripFixture()
	tr.BudgetOverride = intPtr(0)
	b := ledger.TripBudget{Total: 480}

	got := effectiveBudget(tr, b)
	if got != 0 {
		t.Errorf("effectiveBudget with BudgetOverride=&0 = %d, want 0", got)
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

// --- searchBudgetFor: the search-budget invariant ---

// TestSearchBudgetFor_SubtractsOnlyUnbookedStays pins the core invariant: a
// BOOKED stay's points are already reflected in the ledger's used(UY), which
// already reduced Budget.Current, so subtracting them again here would
// double-count. Only unbooked stays' points come off the top.
func TestSearchBudgetFor_SubtractsOnlyUnbookedStays(t *testing.T) {
	tr := tripFixture()
	b := ledger.TripBudget{Total: 610}

	stays := []ledger.TripStay{
		{Points: 150, EntryID: nil},        // unbooked: subtracted
		{Points: 200, EntryID: entryID(1)}, // booked: NOT subtracted
	}
	got := searchBudgetFor(tr, b, stays)
	if got != 460 {
		t.Errorf("searchBudgetFor = %d, want 460 (610 - 150 unbooked, booked 200 excluded)", got)
	}

	// Only booked stays: the answer equals the full budget, nothing
	// subtracted.
	onlyBooked := []ledger.TripStay{
		{Points: 200, EntryID: entryID(1)},
		{Points: 75, EntryID: entryID(2)},
	}
	got2 := searchBudgetFor(tr, b, onlyBooked)
	if got2 != 610 {
		t.Errorf("searchBudgetFor (only booked stays) = %d, want 610 (full budget, nothing subtracted)", got2)
	}
}

// TestSearchBudgetFor_HonoursBudgetOverride proves searchBudgetFor subtracts
// from the trip's BudgetOverride when set, not the computed Total — the same
// override effectiveBudget itself honours.
func TestSearchBudgetFor_HonoursBudgetOverride(t *testing.T) {
	tr := tripFixture()
	tr.BudgetOverride = intPtr(300)
	b := ledger.TripBudget{Total: 610}

	stays := []ledger.TripStay{
		{Points: 50, EntryID: nil},
		{Points: 200, EntryID: entryID(1)},
	}
	got := searchBudgetFor(tr, b, stays)
	if got != 250 {
		t.Errorf("searchBudgetFor = %d, want 250 (override 300 - 50 unbooked)", got)
	}
}

// TestBuildTripView_RemainingMatchesSearchBudget proves buildTripView's
// tripView.Remaining comes from the same searchBudgetFor helper the search
// budget itself is computed from, so the two can never diverge.
func TestBuildTripView_RemainingMatchesSearchBudget(t *testing.T) {
	tr := tripFixture()
	b := ledger.TripBudget{Total: 610}
	stays := []ledger.TripStay{
		{Points: 150, EntryID: nil},
		{Points: 200, EntryID: entryID(1)},
	}

	tv := buildTripView(tr, stays, b, nil, time.January, ledger.CostBasis{}, false)

	want := searchBudgetFor(tr, b, stays)
	if tv.Remaining != want {
		t.Errorf("tv.Remaining = %d, want %d (searchBudgetFor)", tv.Remaining, want)
	}
	if tv.Remaining != 460 {
		t.Errorf("tv.Remaining = %d, want 460", tv.Remaining)
	}
}

// --- stayKeyForStay / Selected marking ---

// TestBuildTripView_MarksCollectedResultRowsSelected proves buildTripView
// marks a result row Selected when it matches a stored stay, and — crucially
// — that a row differing ONLY in Points is NOT marked selected. This is the
// reason stayKey (and its stored-stay counterpart stayKeyForStay) includes
// Points in the identity key: without it, two otherwise-identical rows at
// different point costs would collide.
func TestBuildTripView_MarksCollectedResultRowsSelected(t *testing.T) {
	checkIn := time.Date(2026, 6, 4, 0, 0, 0, 0, time.UTC)
	checkOut := time.Date(2026, 6, 6, 0, 0, 0, 0, time.UTC)

	stays := []ledger.TripStay{
		{
			Resort: "R1", RoomType: "STUDIO", View: "Lake",
			CheckIn: checkIn, CheckOut: checkOut, Nights: 2, Points: 20,
		},
	}
	results := []dvc.StayResult{
		{ // matches the stored stay exactly
			Resort: "R1", RoomType: "STUDIO", View: "Lake",
			CheckIn: checkIn, CheckOut: checkOut, Nights: 2, Points: 20,
		},
		{ // differs ONLY in Points
			Resort: "R1", RoomType: "STUDIO", View: "Lake",
			CheckIn: checkIn, CheckOut: checkOut, Nights: 2, Points: 25,
		},
	}

	tv := buildTripView(tripFixture(), stays, ledger.TripBudget{}, results, time.January, ledger.CostBasis{}, false)

	if len(tv.Results) != 2 {
		t.Fatalf("len(Results) = %d, want 2", len(tv.Results))
	}
	if !tv.Results[0].Selected {
		t.Errorf("Results[0] (exact match) Selected = false, want true")
	}
	if tv.Results[1].Selected {
		t.Errorf("Results[1] (Points differs) Selected = true, want false")
	}
}
