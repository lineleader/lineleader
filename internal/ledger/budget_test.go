package ledger

import "testing"

// TestBudgetForUseYear covers the four golden examples from the owner's
// real portfolio (contracts of 120 and 150 annual points, both April use
// year, so annualPointsTotal = 270), plus the boundary rows in "D".
func TestBudgetForUseYear(t *testing.T) {
	cases := []struct {
		name              string
		summaries         []UseYearSummary
		useYear           int
		annualPointsTotal int
		want              TripBudget
	}{
		{
			// A — healthy: last year banked forward, next year fully
			// unspent and fully borrowable.
			name: "healthy",
			summaries: []UseYearSummary{
				{UseYear: 2025, Allotted: 270, Used: 200, Net: 70},
				{UseYear: 2026, Allotted: 270, Used: 0, Net: 270},
			},
			useYear:           2026,
			annualPointsTotal: 270,
			want: TripBudget{
				UseYear: 2026, Current: 270, Banked: 70, Borrowable: 270, Total: 610,
			},
		},
		{
			// B — the prior use year was already over-borrowed (Net -60).
			// This is THE ENTIRE REASON Banked is signed: clamping it to 0
			// would overstate the budget by exactly the 60 points that were
			// really spent. Total must come out 60 lower than a clamped
			// Banked would give (540 vs the actual 480).
			name: "over-borrowed prior year",
			summaries: []UseYearSummary{
				{UseYear: 2025, Allotted: 270, Used: 330, Net: -60},
				{UseYear: 2026, Allotted: 270, Used: 0, Net: 270},
			},
			useYear:           2026,
			annualPointsTotal: 270,
			want: TripBudget{
				UseYear: 2026, Current: 270, Banked: -60, Borrowable: 270, Total: 480,
			},
		},
		{
			// C — next use year is unposted (Allotted 0, no DistributeNextYear
			// run yet) but already has a trip charged forward against it.
			// Borrowable must shrink by that Used, even with no Allotted.
			name: "next year already committed",
			summaries: []UseYearSummary{
				{UseYear: 2025, Allotted: 0, Used: 0, Net: 0},
				{UseYear: 2026, Allotted: 270, Used: 100, Net: 170},
				{UseYear: 2027, Allotted: 0, Used: 200, Net: -200},
			},
			useYear:           2026,
			annualPointsTotal: 270,
			want: TripBudget{
				UseYear: 2026, Current: 170, Banked: 0, Borrowable: 70, Total: 240,
			},
		},
		{
			name:              "D: no summaries at all",
			summaries:         nil,
			useYear:           2026,
			annualPointsTotal: 270,
			want: TripBudget{
				UseYear: 2026, Current: 0, Banked: 0, Borrowable: 270, Total: 270,
			},
		},
		{
			name:              "D: no contracts and no summaries",
			summaries:         nil,
			useYear:           2026,
			annualPointsTotal: 0,
			want: TripBudget{
				UseYear: 2026, Current: 0, Banked: 0, Borrowable: 0, Total: 0,
			},
		},
		{
			// UY+1's Used exceeds annualPointsTotal: Borrowable clamps at 0
			// rather than going negative.
			name: "D: next year used exceeds annual points",
			summaries: []UseYearSummary{
				{UseYear: 2027, Allotted: 270, Used: 400, Net: -130},
			},
			useYear:           2026,
			annualPointsTotal: 270,
			want: TripBudget{
				UseYear: 2026, Current: 0, Banked: 0, Borrowable: 0, Total: 0,
			},
		},
		{
			// UY+1's Allotted is inflated by bonus/single_use rows (not
			// borrowable) and must be IGNORED — only Used is consulted.
			// Net(2027) here is 950, wildly different from Used (50); a
			// buggy Borrowable = max(0, annualPointsTotal - Net(useYear+1))
			// would give 0 instead of the correct 220.
			name: "D: next year allotted inflated by bonus/single_use rows",
			summaries: []UseYearSummary{
				{UseYear: 2027, Allotted: 1000, Used: 50, Net: 950},
			},
			useYear:           2026,
			annualPointsTotal: 270,
			want: TripBudget{
				UseYear: 2026, Current: 0, Banked: 0, Borrowable: 220, Total: 220,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := BudgetForUseYear(tc.summaries, tc.useYear, tc.annualPointsTotal)
			if got != tc.want {
				t.Errorf("BudgetForUseYear() = %+v, want %+v", got, tc.want)
			}
		})
	}
}
