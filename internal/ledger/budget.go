package ledger

// TripBudget is the point budget available to a trip checking in during UseYear,
// decomposed so the UI can show its arithmetic rather than one unexplained number.
//
// Current and Banked are SIGNED and never clamped: a negative Banked means
// points from UseYear were already charged forward into UseYear-1, and hiding
// that overstates the budget. Total may be negative when the ledger is
// over-spent.
//
// A usage entry is charged to the use year whose points it consumed, not the
// use year its date falls in. The ledger is one pooled chronological list;
// UseYearSummaries partitions it by each entry's UseYear, which the user
// sets explicitly — the CLI documents --year as "override it for points
// drawn from a banked or borrowed use year", and Entry.Tag carries
// "Bank"/"Borrow" as the annotation for that override. BudgetForUseYear
// relies entirely on this convention: it reads UseYear-1 and UseYear+1's
// summaries, not the calendar dates of any entry.
type TripBudget struct {
	UseYear    int
	Current    int // Net(UseYear)
	Banked     int // Net(UseYear-1): positive = banked forward, negative = already borrowed against
	Borrowable int // max(0, annualPointsTotal - Used(UseYear+1))
	Total      int // Current + Banked + Borrowable
}

// BudgetForUseYear is pure: no I/O, no clock, no Postgres — table-testable, and
// its tests never t.Skip.
//
// annualPointsTotal is the sum of every contract's AnnualPoints. It, not the
// posted allotment for UseYear+1, is the borrowing base: a posted allotment can
// include bonus and single_use rows, which are not borrowable, and
// DistributeNextYear only runs one year ahead so UseYear+1 is usually unposted.
//
// There is no 50% borrow cap: that was a temporary COVID-era DVC measure, and
// a member may borrow all of next use year's points at any time. Borrowable
// is the full contractual allotment less whatever's already charged to that
// year — clamped at 0 (a member can't borrow negatively), unlike Current,
// Banked and Total.
//
// Only one year of look-back is consulted, matching DVC's single
// bank-forward rule: residue from UseYear-2 and earlier is deliberately
// dropped. This understates the budget for a chronic under-spender — the
// safe direction; a manual override covers it upstream.
//
// A use year with no matching summary row (never posted, or nothing
// happened in it) contributes zero for both Net and Used, exactly as if a
// zero-valued UseYearSummary were present.
func BudgetForUseYear(summaries []UseYearSummary, useYear, annualPointsTotal int) TripBudget {
	byYear := make(map[int]UseYearSummary, len(summaries))
	for _, s := range summaries {
		byYear[s.UseYear] = s
	}

	current := byYear[useYear].Net
	banked := byYear[useYear-1].Net
	borrowable := annualPointsTotal - byYear[useYear+1].Used
	if borrowable < 0 {
		borrowable = 0
	}

	return TripBudget{
		UseYear:    useYear,
		Current:    current,
		Banked:     banked,
		Borrowable: borrowable,
		Total:      current + banked + borrowable,
	}
}
