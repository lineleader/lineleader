package ledger

import (
	"context"
	"testing"
	"time"
)

// TestStoreTripBudget wires Store.TripBudget (the one I/O boundary of the
// budget model) to a real database, reproducing budget_test.go's case "A"
// end-to-end through real contracts and entries rather than a hand-built
// []UseYearSummary. The two contracts (120 + 150 annual points, both an
// April use year) match the owner's real portfolio, and the entries are
// shaped so UseYearSummaries rolls up to exactly the same Net(2025)=+70,
// Net(2026)=+270 that budget_test.go's case "A" asserts against directly.
func TestStoreTripBudget(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	if _, err := s.AddContract(ctx, Contract{Name: "Point allocation", AnnualPoints: 120, UseYearMonth: time.April}); err != nil {
		t.Fatalf("AddContract: %v", err)
	}
	if _, err := s.AddContract(ctx, Contract{Name: "Point allocation #2", AnnualPoints: 150, UseYearMonth: time.April}); err != nil {
		t.Fatalf("AddContract: %v", err)
	}

	add := func(year int, kind string, allotted, used int) {
		if _, err := s.AddEntry(ctx, Entry{UseYear: year, Date: date(t, "2026-04-01"), Kind: kind, Allotted: allotted, Used: used}); err != nil {
			t.Fatal(err)
		}
	}
	// 2025: Allotted 270, Used 200 -> Net +70 (banked forward).
	add(2025, KindAllocation, 270, 0)
	add(2025, KindUsage, 0, 200)
	// 2026: Allotted 270, Used 0 -> Net +270 (fully unspent, fully borrowable).
	add(2026, KindAllocation, 270, 0)

	got, err := s.TripBudget(ctx, date(t, "2026-06-04"))
	if err != nil {
		t.Fatalf("TripBudget: %v", err)
	}
	want := TripBudget{UseYear: 2026, Current: 270, Banked: 70, Borrowable: 270, Total: 610}
	if got != want {
		t.Errorf("TripBudget() = %+v, want %+v", got, want)
	}
}

// TestStoreTripBudgetUseYearBoundary pins the one thing only TripBudget
// itself can get wrong (BudgetForUseYear is pure and takes useYear as a
// plain int; the crux here is resolving start into the right use year via
// UseYearStartMonth + UseYearForDate). The portfolio's use year starts in
// April, and 2026-01-10 falls BEFORE that start month, so it must resolve
// to use year 2025, not the calendar year 2026 a naive .Year() would give.
func TestStoreTripBudgetUseYearBoundary(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	if _, err := s.AddContract(ctx, Contract{Name: "Point allocation", AnnualPoints: 120, UseYearMonth: time.April}); err != nil {
		t.Fatalf("AddContract: %v", err)
	}
	if _, err := s.AddContract(ctx, Contract{Name: "Point allocation #2", AnnualPoints: 150, UseYearMonth: time.April}); err != nil {
		t.Fatalf("AddContract: %v", err)
	}

	got, err := s.TripBudget(ctx, date(t, "2026-01-10"))
	if err != nil {
		t.Fatalf("TripBudget: %v", err)
	}
	if got.UseYear != 2025 {
		t.Errorf("TripBudget(2026-01-10).UseYear = %d, want 2025 (January is before the April use-year start)", got.UseYear)
	}
}

// TestStoreTripBudgetNoContracts proves TripBudget still works with an
// empty portfolio: UseYearStartMonth falls back to time.January (see its
// own doc comment) when ListContracts returns nothing, and
// annualPointsTotal is 0, so Borrowable must be 0 regardless of what the
// ledger holds. Current/Banked still derive purely from ledger data, so a
// usage entry is added to prove those two numbers aren't silently zeroed
// out along with the contracts.
func TestStoreTripBudgetNoContracts(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	// No contracts. With a January use-year start, 2026-06-13 falls in use
	// year 2026 (see TestUseYearForDate's January case in store_test.go).
	if _, err := s.AddEntry(ctx, Entry{UseYear: 2026, Date: date(t, "2026-06-01"), Kind: KindUsage, Used: 50}); err != nil {
		t.Fatalf("AddEntry: %v", err)
	}

	got, err := s.TripBudget(ctx, date(t, "2026-06-13"))
	if err != nil {
		t.Fatalf("TripBudget: %v", err)
	}
	if got.UseYear != 2026 {
		t.Errorf("UseYear = %d, want 2026 (January-start heuristic with no contracts)", got.UseYear)
	}
	if got.Current != -50 {
		t.Errorf("Current = %d, want -50 (ledger data still consulted with zero contracts)", got.Current)
	}
	if got.Borrowable != 0 {
		t.Errorf("Borrowable = %d, want 0 (annualPointsTotal is 0 with no contracts)", got.Borrowable)
	}
}
