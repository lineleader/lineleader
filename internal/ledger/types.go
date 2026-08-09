// Package ledger persists the DVC points "master ledger": a flat chronological
// list of point allotments and usages whose running balance is derived, not stored.
package ledger

import "time"

// Entry kinds. A usage draws points down (Used > 0); the rest add points (Allotted > 0).
const (
	KindAllocation = "allocation" // recurring annual contract allotment
	KindUsage      = "usage"      // a trip that consumes points
	KindBonus      = "bonus"      // one-off promotional points
	KindSingleUse  = "single_use" // purchased single-use points
	KindAdjustment = "adjustment" // manual correction
)

// Entry is one row of the master ledger (an allocation OR a usage — same list).
type Entry struct {
	ID         int64
	UseYear    int       // the use year this row belongs to; defaulted from Date, editable
	Date       time.Time // transaction date
	Desc       string    // "Point allocation" or a trip name
	Kind       string    // one of the Kind* constants
	Allotted   int       // points added (allocation/bonus/single_use); 0 for a usage
	Used       int       // points consumed (usage); 0 for an allocation
	Tag        string    // free-text annotation: "Bank" | "Borrow" | ""
	ContractID *int64    // set for recurring allocation rows (Distribute idempotency); nil otherwise

	// RunningBalance is the cumulative (Allotted-Used) up to and including this row,
	// ordered by (Date, ID). It is computed by ListEntries, never stored.
	RunningBalance int
}

// Contract is a template that drives the Distribute action. It is not part of the
// running balance itself — the balance is pooled across all contracts.
type Contract struct {
	ID            int64
	Name          string     // "Point allocation", "Point allocation #2"
	Number        string     // DVC contract/membership number (free text)
	HomeResort    string     // optional resort code
	AnnualPoints  int        // 120, 150, …
	UseYearMonth  time.Month // Use Year start month; anchors the allocation date and UseYearForDate
	TermYears     int        // contract length in years; 0 means cost data hasn't been entered
	PurchasePrice Cents      // purchase price paid; 0 means cost data hasn't been entered
	ClosingCosts  Cents      // closing costs paid; 0 means cost data hasn't been entered
}

// UseYearSummary rolls up every entry sharing a use year.
type UseYearSummary struct {
	UseYear      int
	Allotted     int
	Used         int
	Net          int  // Allotted - Used
	OverBorrowed bool // Net < 0
}

// UseYearForDate returns the use year a date falls in for a contract whose use year
// starts on startMonth. With an April start, 2026-02-15 belongs to use year 2025
// (use year 2026 does not begin until April 2026).
func UseYearForDate(date time.Time, startMonth time.Month) int {
	if date.Month() >= startMonth {
		return date.Year()
	}
	return date.Year() - 1
}
