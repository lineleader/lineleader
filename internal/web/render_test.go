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
// filterScope, projects the trip scope carrying TripID and Mode.
func TestToFiltersView_TripScope(t *testing.T) {
	fv := toFiltersView(dvc.FilterOptionsView{
		Resorts:   []dvc.ResortOption{{Code: "TST", Name: "Test Resort", Enabled: false}},
		RoomTypes: []dvc.RoomTypeOption{{Name: "Studio", Enabled: true}},
	}, filterScope{IsTrip: true, TripID: 1, Mode: dvc.FilterModeOverride})

	if !fv.Scope.IsTrip {
		t.Errorf("Scope.IsTrip = false, want true for trip")
	}
	if fv.Scope.TripID != 1 {
		t.Errorf("Scope.TripID = %d, want 1", fv.Scope.TripID)
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

// TestToFiltersView_TripScopeCarriesIDAndName proves the trip scope's ID and
// Name both make it through toFiltersView unchanged — filterTitle depends on
// TripName, and the per-trip template URLs depend on TripID.
func TestToFiltersView_TripScopeCarriesIDAndName(t *testing.T) {
	fv := toFiltersView(dvc.FilterOptionsView{}, filterScope{
		IsTrip:   true,
		TripID:   7,
		TripName: "Beach week",
		Mode:     dvc.FilterModeOverride,
	})
	if fv.Scope.TripID != 7 {
		t.Errorf("Scope.TripID = %d, want 7", fv.Scope.TripID)
	}
	if fv.Scope.TripName != "Beach week" {
		t.Errorf("Scope.TripName = %q, want %q", fv.Scope.TripName, "Beach week")
	}
}

// TestFilterTitle_UsesTripName proves filterTitle renders the trip's actual
// name (not "Trip N" numbering) for both trip modes, and leaves the global
// title alone.
func TestFilterTitle_UsesTripName(t *testing.T) {
	filterTitle := templateFuncs()["filterTitle"].(func(filterScope) string)

	if got := filterTitle(filterScope{}); got != "Filters — Global" {
		t.Errorf("global title = %q, want %q", got, "Filters — Global")
	}

	inherit := filterTitle(filterScope{IsTrip: true, TripID: 42, TripName: "Fall trip", Mode: dvc.FilterModeInherit})
	if !strings.Contains(inherit, "Fall trip") {
		t.Errorf("inherit title = %q, want to contain the trip name", inherit)
	}
	if !strings.Contains(inherit, "inherit") {
		t.Errorf("inherit title = %q, want to mention inherit mode", inherit)
	}
	if strings.Contains(inherit, "Trip 42") || strings.Contains(inherit, "Trip 43") {
		t.Errorf("inherit title = %q, want no Trip N numbering", inherit)
	}

	override := filterTitle(filterScope{IsTrip: true, TripID: 42, TripName: "Fall trip", Mode: dvc.FilterModeOverride})
	if !strings.Contains(override, "Fall trip") {
		t.Errorf("override title = %q, want to contain the trip name", override)
	}
	if !strings.Contains(override, "override") {
		t.Errorf("override title = %q, want to mention override mode", override)
	}
	if strings.Contains(override, "Trip 42") || strings.Contains(override, "Trip 43") {
		t.Errorf("override title = %q, want no Trip N numbering", override)
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

// TestBuildTripView_HasBookedAndUnbookedFlags mirrors
// TestBuildTripView_DerivesStatusFromEntryID's table exactly (same four stay
// shapes) but asserts the two new HasBookedStays/HasUnbookedStays flags
// buildTripView must expose. These flags exist because Booked/PartlyBooked
// alone are ambiguous for the template's book_controls sub-template: a
// trip with Booked=false && PartlyBooked=false could mean EITHER "no stays
// at all" OR "one or more stays, all unbooked" — and only the latter case
// should render a "Book it" button. HasBookedStays/HasUnbookedStays are
// exact synonyms for the anyBooked/anyUnbooked booleans computed once from
// stays' EntryID, so they resolve that ambiguity directly rather than
// asking a caller to reverse-engineer it from the other three fields.
func TestBuildTripView_HasBookedAndUnbookedFlags(t *testing.T) {
	cases := []struct {
		name                 string
		stays                []ledger.TripStay
		wantBooked           bool
		wantPartlyBooked     bool
		wantHasBookedStays   bool
		wantHasUnbookedStays bool
	}{
		{
			name: "no stays",
			// Neither flag set: there is nothing to book or unbook.
		},
		{
			name: "all unbooked",
			stays: []ledger.TripStay{
				{Points: 10, EntryID: nil},
				{Points: 20, EntryID: nil},
			},
			wantHasUnbookedStays: true,
		},
		{
			name: "all booked",
			stays: []ledger.TripStay{
				{Points: 10, EntryID: entryID(1)},
				{Points: 20, EntryID: entryID(2)},
			},
			wantBooked:         true,
			wantHasBookedStays: true,
		},
		{
			name: "mixed",
			stays: []ledger.TripStay{
				{Points: 10, EntryID: entryID(1)},
				{Points: 20, EntryID: nil},
			},
			wantPartlyBooked:     true,
			wantHasBookedStays:   true,
			wantHasUnbookedStays: true,
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
			if tv.HasBookedStays != c.wantHasBookedStays {
				t.Errorf("HasBookedStays = %v, want %v", tv.HasBookedStays, c.wantHasBookedStays)
			}
			if tv.HasUnbookedStays != c.wantHasUnbookedStays {
				t.Errorf("HasUnbookedStays = %v, want %v", tv.HasUnbookedStays, c.wantHasUnbookedStays)
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

// TestBuildTripView_OverriddenBudgetSetsEffectiveAndComputed proves an
// override's effect reaches the whole tripView, not just EffectiveBudget in
// isolation: Budget.ComputedTotal keeps the ledger total, Budget.Overridden
// flips true, and — the part TestBuildTripView_EffectiveBudgetHonoursOverride
// doesn't cover — Remaining subtracts unbooked stays' points from the
// OVERRIDE, not from the computed total.
func TestBuildTripView_OverriddenBudgetSetsEffectiveAndComputed(t *testing.T) {
	tr := tripFixture()
	tr.BudgetOverride = intPtr(100)
	b := ledger.TripBudget{Total: 480}
	stays := []ledger.TripStay{
		{Points: 30, EntryID: nil},         // unbooked: subtracted
		{Points: 200, EntryID: entryID(1)}, // booked: not subtracted
	}

	tv := buildTripView(tr, stays, b, nil, time.January, ledger.CostBasis{}, false)

	if tv.EffectiveBudget != 100 {
		t.Errorf("EffectiveBudget = %d, want 100 (the override)", tv.EffectiveBudget)
	}
	if tv.Budget.ComputedTotal != 480 {
		t.Errorf("Budget.ComputedTotal = %d, want 480 (still the ledger total)", tv.Budget.ComputedTotal)
	}
	if !tv.Budget.Overridden {
		t.Errorf("Budget.Overridden = false, want true")
	}
	if tv.Remaining != 70 {
		t.Errorf("Remaining = %d, want 70 (100 override - 30 unbooked points) — Remaining must subtract from the OVERRIDE, not the computed total (which would give 450)", tv.Remaining)
	}
}

// TestBuildTripView_ZeroOverrideIsHonoured guards against a `> 0` or
// truthiness check creeping into buildTripView's override handling: an
// override of exactly 0 is a legitimate, honoured budget (see
// effectiveBudget's doc comment), not treated as "no override".
func TestBuildTripView_ZeroOverrideIsHonoured(t *testing.T) {
	tr := tripFixture()
	tr.BudgetOverride = intPtr(0)
	b := ledger.TripBudget{Total: 480}

	tv := buildTripView(tr, nil, b, nil, time.January, ledger.CostBasis{}, false)

	if tv.EffectiveBudget != 0 {
		t.Errorf("EffectiveBudget = %d, want 0", tv.EffectiveBudget)
	}
	if !tv.Budget.Overridden {
		t.Errorf("Budget.Overridden = false, want true — a 0 override is still an override")
	}
	if !tv.BudgetOverridden {
		t.Errorf("BudgetOverridden = false, want true")
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

// --- result truncation (lineleader-ixe.12) ---

// manyResults builds n dvc.StayResult values with ascending Points, so a
// test can tell truncated rows apart by their point cost and confirm order.
func manyResults(n int) []dvc.StayResult {
	checkIn := time.Date(2026, 6, 4, 0, 0, 0, 0, time.UTC)
	checkOut := time.Date(2026, 6, 6, 0, 0, 0, 0, time.UTC)
	results := make([]dvc.StayResult, n)
	for i := 0; i < n; i++ {
		results[i] = dvc.StayResult{
			Resort:   "R1",
			RoomType: "STUDIO",
			View:     "Lake",
			CheckIn:  checkIn,
			CheckOut: checkOut,
			Nights:   2,
			Points:   i, // ascending — distinguishes rows and matches Search's own order
		}
	}
	return results
}

// TestBuildTripView_TruncatesResultsAtMaxResultRows proves buildTripView caps
// tv.Results at maxResultRows while ResultsTotal keeps the PRE-truncation
// count, so the template can report how many stays were hidden.
func TestBuildTripView_TruncatesResultsAtMaxResultRows(t *testing.T) {
	results := manyResults(maxResultRows + 25)

	tv := buildTripView(tripFixture(), nil, ledger.TripBudget{}, results, time.January, ledger.CostBasis{}, false)

	if len(tv.Results) != maxResultRows {
		t.Errorf("len(Results) = %d, want %d", len(tv.Results), maxResultRows)
	}
	if tv.ResultsTotal != maxResultRows+25 {
		t.Errorf("ResultsTotal = %d, want %d", tv.ResultsTotal, maxResultRows+25)
	}
}

// TestBuildTripView_UnderCapIsNotTruncated proves a result set smaller than
// maxResultRows passes through unchanged, and ResultsTotal equals the
// (untruncated) length — so the template's ResultsTotal > len(Results)
// notice condition is false and nothing renders.
func TestBuildTripView_UnderCapIsNotTruncated(t *testing.T) {
	results := manyResults(maxResultRows - 10)

	tv := buildTripView(tripFixture(), nil, ledger.TripBudget{}, results, time.January, ledger.CostBasis{}, false)

	if len(tv.Results) != maxResultRows-10 {
		t.Errorf("len(Results) = %d, want %d", len(tv.Results), maxResultRows-10)
	}
	if tv.ResultsTotal != maxResultRows-10 {
		t.Errorf("ResultsTotal = %d, want %d", tv.ResultsTotal, maxResultRows-10)
	}
}

// TestBuildTripView_TruncationKeepsThePrefixInOrder proves truncation is a
// plain PREFIX slice of the ordered results — never a re-sort, filter, or
// tail — and that each retained row's RowIndex still matches its position in
// the untruncated slice. This is the invariant addStay depends on: it
// resolves {row} against a freshly re-run, untruncated search, so a
// truncated row's RowIndex must still point at the same result there.
func TestBuildTripView_TruncationKeepsThePrefixInOrder(t *testing.T) {
	results := manyResults(maxResultRows + 25)

	tv := buildTripView(tripFixture(), nil, ledger.TripBudget{}, results, time.January, ledger.CostBasis{}, false)

	if len(tv.Results) != maxResultRows {
		t.Fatalf("len(Results) = %d, want %d", len(tv.Results), maxResultRows)
	}
	for i := 0; i < maxResultRows; i++ {
		if tv.Results[i].RowIndex != i {
			t.Errorf("Results[%d].RowIndex = %d, want %d", i, tv.Results[i].RowIndex, i)
		}
		if tv.Results[i].Points != results[i].Points {
			t.Errorf("Results[%d].Points = %d, want %d (results[%d])", i, tv.Results[i].Points, results[i].Points, i)
		}
	}
}
