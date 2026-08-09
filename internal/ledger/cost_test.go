package ledger

import (
	"testing"
	"time"
)

// contract2019 and contract2022 are the two golden contracts from the
// owner's "Point cost" sheet, used throughout cost_test.go.
var (
	contract2019 = Contract{
		ID:            1,
		AnnualPoints:  120,
		UseYearMonth:  time.April,
		TermYears:     44,
		PurchasePrice: 2_940_000, // $29,400.00
		ClosingCosts:  58_835,    // $588.35
	}
	contract2022 = Contract{
		ID:            2,
		AnnualPoints:  150,
		UseYearMonth:  time.April,
		TermYears:     41,
		PurchasePrice: 3_015_000, // $30,150.00
		ClosingCosts:  66_500,    // $665.00
	}
)

// TestCostBasisBlended pins the blended-rate golden value and RateFor's
// per-contract-with-blended-fallback behaviour: an unpriced or unknown
// contract ID falls back to the blended rate, never to zero.
func TestCostBasisBlended(t *testing.T) {
	unpriced := Contract{ID: 3, AnnualPoints: 100, UseYearMonth: time.April} // no cost data yet
	noPoints := Contract{                                                    // annual_points == 0: excluded from the blend even though "priced"
		ID: 4, AnnualPoints: 0, UseYearMonth: time.April,
		TermYears: 10, PurchasePrice: 100_000, ClosingCosts: 1_000,
	}

	b := NewCostBasis([]Contract{contract2019, contract2022, unpriced, noPoints}, nil)

	if got := b.Blended(); got != 5_307_921 {
		t.Errorf("Blended() = %d, want 5_307_921", got)
	}
	if got := b.RateFor(&contract2019.ID); got != 5_679_612 {
		t.Errorf("RateFor(2019) = %d, want 5_679_612", got)
	}
	if got := b.RateFor(&contract2022.ID); got != 5_010_569 {
		t.Errorf("RateFor(2022) = %d, want 5_010_569", got)
	}
	if got := b.RateFor(&unpriced.ID); got != b.Blended() {
		t.Errorf("RateFor(unpriced) = %d, want blended %d", got, b.Blended())
	}
	if got := b.RateFor(nil); got != b.Blended() {
		t.Errorf("RateFor(nil) = %d, want blended %d", got, b.Blended())
	}
	var unknownID int64 = 999
	if got := b.RateFor(&unknownID); got != b.Blended() {
		t.Errorf("RateFor(unknown id) = %d, want blended %d", got, b.Blended())
	}
}

// TestCostBasisBlendedNoContracts covers the "no priced contracts" edge:
// blended is 0 and hasRate is false, which downstream Cost() gates on via
// Known() rather than treating 0 as a real rate.
func TestCostBasisBlendedNoContracts(t *testing.T) {
	b := NewCostBasis(nil, nil)
	if got := b.Blended(); got != 0 {
		t.Errorf("Blended() with no contracts = %d, want 0", got)
	}
	if got := b.RateFor(nil); got != 0 {
		t.Errorf("RateFor(nil) with no contracts = %d, want 0", got)
	}
}

// seededDues is the owner's eight-year dues series, in Micros — the same
// values seed.sql inserts.
var seededDues = []DuesRate{
	{2019, 6_385_000},
	{2020, 6_561_600},
	{2021, 6_811_800},
	{2022, 7_007_700},
	{2023, 7_333_200},
	{2024, 7_574_000},
	{2025, 7_929_800},
	{2026, 8_223_500},
}

// TestCostBasisKnown covers every combination of "has a priced contract" x
// "has a dues rate": both are required for Known().
func TestCostBasisKnown(t *testing.T) {
	cases := []struct {
		name      string
		contracts []Contract
		dues      []DuesRate
		want      bool
	}{
		{"neither", nil, nil, false},
		{"rate only", []Contract{contract2019}, nil, false},
		{"dues only", nil, seededDues, false},
		{"both", []Contract{contract2019}, seededDues, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := NewCostBasis(c.contracts, c.dues).Known(); got != c.want {
				t.Errorf("Known() = %v, want %v", got, c.want)
			}
		})
	}
}

// TestDuesForStored covers every stored year: exact rate, projected=false.
func TestDuesForStored(t *testing.T) {
	b := NewCostBasis(nil, seededDues)
	for _, d := range seededDues {
		rate, projected := b.DuesFor(d.UseYear)
		if rate != d.Rate || projected {
			t.Errorf("DuesFor(%d) = (%d, %v), want (%d, false)", d.UseYear, rate, projected, d.Rate)
		}
	}
}

// TestDuesForProjectedForward pins the two golden projected years beyond
// the stored series: growthMicros for the seeded series is 1_036_836 (the
// mean YoY ratio across the 7 adjacent-year pairs), and compounding it
// forward one year at a time from 2026 gives these values.
func TestDuesForProjectedForward(t *testing.T) {
	b := NewCostBasis(nil, seededDues)
	cases := []struct {
		year int
		want Micros
	}{
		{2027, 8_526_421},
		{2028, 8_840_500},
	}
	for _, c := range cases {
		rate, projected := b.DuesFor(c.year)
		if rate != c.want || !projected {
			t.Errorf("DuesFor(%d) = (%d, %v), want (%d, true)", c.year, rate, projected, c.want)
		}
	}
}

// TestDuesForGrowthProjection exercises backward projection and an interior
// gap against a small, hand-computable dues series (2020: $1.00, 2021:
// $1.10, 2023: $1.331) instead of the seeded one, so every expected value
// below is checked by hand rather than taken from the implementation.
//
// Only the (2020, 2021) pair is one year apart, so growthMicros is that
// single ratio: divRound(1_100_000 * 1_000_000, 1_000_000) = 1_100_000 (the
// (2021, 2023) pair spans two years and is skipped, per spec).
func TestDuesForGrowthProjection(t *testing.T) {
	b := NewCostBasis(nil, []DuesRate{
		{2020, 1_000_000},
		{2021, 1_100_000},
		{2023, 1_331_000},
	})

	cases := []struct {
		name     string
		year     int
		wantRate Micros
		wantProj bool
	}{
		// Backward, one step past firstYear (2020):
		// divRound(1_000_000 * 1_000_000, 1_100_000) = 909_091.
		{"backward one year", 2019, 909_091, true},
		// Interior gap: nearest stored year below 2022 is 2021 ($1.10);
		// one forward compounding step: divRound(1_100_000*1_100_000, 1e6) = 1_210_000.
		{"interior gap", 2022, 1_210_000, true},
		// Forward, one step past lastYear (2023):
		// divRound(1_331_000 * 1_100_000, 1_000_000) = 1_464_100.
		{"forward one year", 2024, 1_464_100, true},
		// A stored year is exact, not projected.
		{"stored", 2021, 1_100_000, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rate, projected := b.DuesFor(c.year)
			if rate != c.wantRate || projected != c.wantProj {
				t.Errorf("DuesFor(%d) = (%d, %v), want (%d, %v)", c.year, rate, projected, c.wantRate, c.wantProj)
			}
		})
	}
}

// TestDuesForSparseSeries covers the growthMicros edge cases: 0 or 1 stored
// rows means "flat" (growthMicros = 1_000_000), and 0 rows means DuesFor has
// nothing to report at all.
func TestDuesForSparseSeries(t *testing.T) {
	t.Run("no dues rows", func(t *testing.T) {
		b := NewCostBasis(nil, nil)
		rate, projected := b.DuesFor(2025)
		if rate != 0 || projected {
			t.Errorf("DuesFor(2025) with no dues = (%d, %v), want (0, false)", rate, projected)
		}
	})
	t.Run("single dues row is flat", func(t *testing.T) {
		b := NewCostBasis(nil, []DuesRate{{2020, 1_000_000}})
		// Flat growth: projecting forward or backward from the one stored
		// year returns that same rate, still flagged projected.
		if rate, projected := b.DuesFor(2021); rate != 1_000_000 || !projected {
			t.Errorf("DuesFor(2021) = (%d, %v), want (1_000_000, true)", rate, projected)
		}
		if rate, projected := b.DuesFor(2019); rate != 1_000_000 || !projected {
			t.Errorf("DuesFor(2019) = (%d, %v), want (1_000_000, true)", rate, projected)
		}
	})
}

// TestCostBasisCost pins the two golden priced stays from the design doc
// (a per-contract rate against a stored dues year, and a blended rate
// against another stored dues year) plus the "not priceable" and
// "projected dues year" edge cases.
func TestCostBasisCost(t *testing.T) {
	b := NewCostBasis([]Contract{contract2019, contract2022}, seededDues)

	t.Run("per-contract rate, stored dues", func(t *testing.T) {
		cost, projected, known := b.Cost(100, 2025, &contract2019.ID)
		if cost != 136_094 || projected || !known {
			t.Errorf("Cost(100, 2025, 2019) = (%d, %v, %v), want (136_094, false, true)", cost, projected, known)
		}
	})

	t.Run("blended rate, stored dues", func(t *testing.T) {
		cost, projected, known := b.Cost(240, 2021, nil)
		if cost != 290_873 || projected || !known {
			t.Errorf("Cost(240, 2021, nil) = (%d, %v, %v), want (290_873, false, true)", cost, projected, known)
		}
	})

	t.Run("projected dues year propagates the flag", func(t *testing.T) {
		cost, projected, known := b.Cost(100, 2027, nil)
		if cost == 0 || !projected || !known {
			t.Errorf("Cost(100, 2027, nil) = (%d, %v, %v), want (nonzero, true, true)", cost, projected, known)
		}
		// PointCost(100, blended, DuesFor(2027)) is the same computation
		// Cost should have performed; cross-check it directly.
		dues, duesProjected := b.DuesFor(2027)
		want := PointCost(100, b.Blended(), dues)
		if cost != want || !duesProjected {
			t.Errorf("Cost(100, 2027, nil) = %d, want %d (DuesFor projected=%v)", cost, want, duesProjected)
		}
	})

	t.Run("points <= 0 is not priced", func(t *testing.T) {
		for _, points := range []int{0, -5} {
			cost, projected, known := b.Cost(points, 2025, nil)
			if cost != 0 || projected || known {
				t.Errorf("Cost(%d, 2025, nil) = (%d, %v, %v), want (0, false, false)", points, cost, projected, known)
			}
		}
	})

	t.Run("unknown basis is not priced", func(t *testing.T) {
		unknown := NewCostBasis(nil, nil)
		cost, projected, known := unknown.Cost(100, 2025, nil)
		if cost != 0 || projected || known {
			t.Errorf("Cost on unknown basis = (%d, %v, %v), want (0, false, false)", cost, projected, known)
		}
	})
}

// TestCostBasisUseYearMonth pins the UseYearMonth heuristic: the first
// contract's UseYearMonth (ListContracts order, i.e. by id), or January
// when there are no contracts.
func TestCostBasisUseYearMonth(t *testing.T) {
	aug := Contract{ID: 5, UseYearMonth: time.August}
	b := NewCostBasis([]Contract{aug, contract2019}, nil)
	if got := b.UseYearMonth(); got != time.August {
		t.Errorf("UseYearMonth() = %v, want %v (first contract's)", got, time.August)
	}

	empty := NewCostBasis(nil, nil)
	if got := empty.UseYearMonth(); got != time.January {
		t.Errorf("UseYearMonth() with no contracts = %v, want %v", got, time.January)
	}
}

// TestContractPricePerPointYear pins the two golden contracts from the
// owner's "Point cost" sheet, plus the "cost unknown" edge cases: zero in
// term_years, purchase price or closing costs (or annual_points, which
// would otherwise divide by zero) means the rate cannot be derived.
func TestContractPricePerPointYear(t *testing.T) {
	cases := []struct {
		name string
		c    Contract
		want Micros
		ok   bool
	}{
		{
			name: "2019 contract",
			c: Contract{
				AnnualPoints:  120,
				TermYears:     44,
				PurchasePrice: 2_940_000, // $29,400.00
				ClosingCosts:  58_835,    // $588.35
			},
			want: 5_679_612,
			ok:   true,
		},
		{
			name: "2022 contract",
			c: Contract{
				AnnualPoints:  150,
				TermYears:     41,
				PurchasePrice: 3_015_000, // $30,150.00
				ClosingCosts:  66_500,    // $665.00
			},
			want: 5_010_569,
			ok:   true,
		},
		{
			name: "term_years zero (pre-backfill)",
			c: Contract{
				AnnualPoints:  120,
				TermYears:     0,
				PurchasePrice: 2_940_000,
				ClosingCosts:  58_835,
			},
			want: 0,
			ok:   false,
		},
		{
			name: "purchase price zero",
			c: Contract{
				AnnualPoints:  120,
				TermYears:     44,
				PurchasePrice: 0,
				ClosingCosts:  58_835,
			},
			want: 0,
			ok:   false,
		},
		{
			name: "closing costs zero",
			c: Contract{
				AnnualPoints:  120,
				TermYears:     44,
				PurchasePrice: 2_940_000,
				ClosingCosts:  0,
			},
			want: 0,
			ok:   false,
		},
		{
			name: "annual points zero",
			c: Contract{
				AnnualPoints:  0,
				TermYears:     44,
				PurchasePrice: 2_940_000,
				ClosingCosts:  58_835,
			},
			want: 0,
			ok:   false,
		},
		{
			name: "everything zero (fresh contract)",
			c:    Contract{},
			want: 0,
			ok:   false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := c.c.PricePerPointYear()
			if got != c.want || ok != c.ok {
				t.Errorf("PricePerPointYear() = (%v, %v), want (%v, %v)", got, ok, c.want, c.ok)
			}
		})
	}
}
