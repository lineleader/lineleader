package ledger

import "testing"

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
