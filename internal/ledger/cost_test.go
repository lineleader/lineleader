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
