package ledger

import (
	"errors"
	"testing"
	"time"
)

// TestAllocateStayPointsSplitsAcrossContracts is the headline behaviour: a
// stay that exceeds one contract's remaining balance spills into the next
// candidate in rank order, drawing exactly what it needs from each.
func TestAllocateStayPointsSplitsAcrossContracts(t *testing.T) {
	contracts := []Contract{
		{ID: 1, UseYearMonth: time.April},
		{ID: 2, UseYearMonth: time.April},
	}
	lots := []PointLot{
		{ContractID: 1, UseYear: 2026, Points: 95},
		{ContractID: 2, UseYear: 2026, Points: 100},
	}

	got, err := AllocateStayPoints(contracts, lots, nil, day("2026-06-01"), 150)
	if err != nil {
		t.Fatal(err)
	}
	want := []PointDraw{
		{ContractID: 1, UseYear: 2026, Disposition: DispositionCurrent, Points: 95},
		{ContractID: 2, UseYear: 2026, Disposition: DispositionCurrent, Points: 55},
	}
	if len(got) != len(want) {
		t.Fatalf("draws = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("draw[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestAllocateStayPointsAcrossUseYearMonths proves eligibility is computed
// per contract: the same check-in date resolves to a different current use
// year for each contract because their UseYearMonth differs.
func TestAllocateStayPointsAcrossUseYearMonths(t *testing.T) {
	contracts := []Contract{
		{ID: 1, UseYearMonth: time.April},
		{ID: 2, UseYearMonth: time.December},
	}
	lots := []PointLot{
		// Contract 1 (April use year): 2026-11-30 is use year 2026, so 2025
		// is banked, 2026 current, 2027 borrowed.
		{ContractID: 1, UseYear: 2025, Points: 4},
		{ContractID: 1, UseYear: 2026, Points: 20},
		{ContractID: 1, UseYear: 2027, Points: 20},
		// Contract 2 (December use year): 2026-11-30 is still use year 2025,
		// so 2025 is current for contract 2 even though the calendar date is
		// identical to contract 1's night above.
		{ContractID: 2, UseYear: 2024, Points: 20},
		{ContractID: 2, UseYear: 2025, Points: 20},
	}

	got, err := AllocateStayPoints(contracts, lots, nil, day("2026-11-30"), 4)
	if err != nil {
		t.Fatal(err)
	}
	// Contract 1's banked lot (2025, 4 points) ranks ahead of contract 2's
	// current lot (2025, 20 points) since banked (rank 0) beats current
	// (rank 1) regardless of contract id.
	want := []PointDraw{
		{ContractID: 1, UseYear: 2025, Disposition: DispositionBanked, Points: 4},
	}
	if len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("draws = %+v, want %+v", got, want)
	}
}

// TestAllocateStayPointsDeterministicOrder pins the sort: banked before
// current before borrowed, tied broken by ContractID ascending, regardless
// of the order contracts/lots are passed in. It also proves that draining
// the banked lot via consumed falls through to borrowed.
func TestAllocateStayPointsDeterministicOrder(t *testing.T) {
	contracts := []Contract{
		{ID: 2, UseYearMonth: time.April},
		{ID: 1, UseYearMonth: time.April},
	}
	lots := []PointLot{
		{ContractID: 2, UseYear: 2025, Points: 3},
		{ContractID: 1, UseYear: 2025, Points: 3},
		{ContractID: 1, UseYear: 2026, Points: 3},
		{ContractID: 1, UseYear: 2027, Points: 5},
	}

	got, err := AllocateStayPoints(contracts, lots, nil, day("2026-06-01"), 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("draws = %+v", got)
	}
	if got[0].ContractID != 1 || got[0].UseYear != 2025 || got[0].Disposition != DispositionBanked {
		t.Errorf("draw[0] = %+v, want banked contract 1", got[0])
	}
	if got[1].ContractID != 2 || got[1].UseYear != 2025 || got[1].Disposition != DispositionBanked {
		t.Errorf("draw[1] = %+v, want banked contract 2", got[1])
	}
	if got[2].ContractID != 1 || got[2].UseYear != 2026 || got[2].Disposition != DispositionCurrent {
		t.Errorf("draw[2] = %+v, want current contract 1", got[2])
	}

	// Drain the banked lots via consumed so allocation falls through to
	// borrowed.
	consumed := []PointDraw{
		{ContractID: 1, UseYear: 2025, Points: 3},
		{ContractID: 2, UseYear: 2025, Points: 3},
	}
	got, err = AllocateStayPoints(contracts, lots, consumed, day("2026-06-01"), 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("draws = %+v", got)
	}
	if got[0].UseYear != 2026 || got[0].Disposition != DispositionCurrent || got[0].Points != 3 {
		t.Errorf("draw[0] = %+v, want current 3 points", got[0])
	}
	if got[1].UseYear != 2027 || got[1].Disposition != DispositionBorrowed || got[1].Points != 5 {
		t.Errorf("draw[1] = %+v, want borrowed 5 points", got[1])
	}
}

// TestAllocateStayPointsInsufficientDoesNotReturnPartial asserts that when
// a stay can't be fully funded, the function returns a nil slice (never a
// partial allocation) alongside ErrInsufficientPoints.
func TestAllocateStayPointsInsufficientDoesNotReturnPartial(t *testing.T) {
	contracts := []Contract{{ID: 1, UseYearMonth: time.October}}
	lots := []PointLot{{ContractID: 1, UseYear: 2025, Points: 5}}

	got, err := AllocateStayPoints(contracts, lots, nil, day("2026-09-30"), 6)
	if !errors.Is(err, ErrInsufficientPoints) {
		t.Fatalf("error = %v, want ErrInsufficientPoints", err)
	}
	if got != nil {
		t.Fatalf("partial allocation returned: %+v", got)
	}
}

// TestAllocateStayPointsZeroOrNegativePoints pins that a stay costing
// nothing needs no draws and is not an error.
func TestAllocateStayPointsZeroOrNegativePoints(t *testing.T) {
	contracts := []Contract{{ID: 1, UseYearMonth: time.April}}
	lots := []PointLot{{ContractID: 1, UseYear: 2026, Points: 100}}

	for _, points := range []int{0, -10} {
		got, err := AllocateStayPoints(contracts, lots, nil, day("2026-06-01"), points)
		if err != nil {
			t.Fatalf("points=%d: err = %v, want nil", points, err)
		}
		if got != nil {
			t.Fatalf("points=%d: draws = %+v, want nil", points, got)
		}
	}
}

// TestAllocateStayPointsCoalescesDuplicateLotRows ensures that multiple
// PointLot rows sharing the same (ContractID, UseYear) are summed into one
// candidate and produce exactly one PointDraw against that key, never one
// draw per row.
func TestAllocateStayPointsCoalescesDuplicateLotRows(t *testing.T) {
	contracts := []Contract{{ID: 1, UseYearMonth: time.April}}
	lots := []PointLot{
		{ContractID: 1, UseYear: 2026, Points: 40},
		{ContractID: 1, UseYear: 2026, Points: 50},
	}

	// Needs less than the summed balance (90): one draw, partial.
	got, err := AllocateStayPoints(contracts, lots, nil, day("2026-06-01"), 60)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("partial: draws = %+v, want 1 draw", got)
	}
	if got[0] != (PointDraw{ContractID: 1, UseYear: 2026, Disposition: DispositionCurrent, Points: 60}) {
		t.Errorf("partial: draw = %+v, want 60 points", got[0])
	}

	// Needs exactly the summed balance (90): one draw, full.
	got, err = AllocateStayPoints(contracts, lots, nil, day("2026-06-01"), 90)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("exact: draws = %+v, want 1 draw", got)
	}
	if got[0] != (PointDraw{ContractID: 1, UseYear: 2026, Disposition: DispositionCurrent, Points: 90}) {
		t.Errorf("exact: draw = %+v, want 90 points", got[0])
	}

	// Needs more than the summed balance: insufficient, nil slice.
	got, err = AllocateStayPoints(contracts, lots, nil, day("2026-06-01"), 100)
	if !errors.Is(err, ErrInsufficientPoints) {
		t.Fatalf("insufficient: error = %v, want ErrInsufficientPoints", err)
	}
	if got != nil {
		t.Fatalf("insufficient: partial allocation returned: %+v", got)
	}
}

func day(s string) time.Time {
	t, err := time.Parse(DateLayout, s)
	if err != nil {
		panic(err)
	}
	return t
}
