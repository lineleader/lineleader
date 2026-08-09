package ledger

import "time"

// CostBasis is a pure, immutable pricing snapshot: everything needed to
// price a stay in points as a dollar cost, derived once from the contracts
// and dues rates in the database. It performs no I/O itself — see
// Store.CostBasis for the one place that reads the database — which makes
// every method below table-driven-testable with no Postgres involved.
type CostBasis struct {
	rates   map[int64]Micros // contract id -> $/pt/yr, priced contracts only
	blended Micros
	hasRate bool

	dues                map[int]Micros
	firstYear, lastYear int
	growthMicros        int64 // mean YoY dues ratio x 1e6
	hasDues             bool

	useYearMonth time.Month
}

// NewCostBasis derives a CostBasis from every contract and stored dues rate.
// contracts should be ListContracts order (ascending by id) — UseYearMonth
// uses the first entry as its heuristic anchor.
func NewCostBasis(contracts []Contract, dues []DuesRate) CostBasis {
	b := CostBasis{
		rates:        make(map[int64]Micros),
		useYearMonth: time.January,
	}
	if len(contracts) > 0 {
		b.useYearMonth = contracts[0].UseYearMonth
	}

	var num, den int64
	for _, c := range contracts {
		rate, ok := c.PricePerPointYear()
		if !ok {
			continue
		}
		b.rates[c.ID] = rate
		num += int64(c.AnnualPoints) * int64(rate)
		den += int64(c.AnnualPoints)
	}
	if den > 0 {
		b.blended = Micros(divRound(num, den))
		b.hasRate = true
	}

	_ = dues // dues processing lands in a later commit

	return b
}

// Blended is the portfolio-wide $/pt/yr rate: the annual-points-weighted
// mean of every priced contract's PricePerPointYear. It is 0 when no
// contract is priced (see Known).
func (b CostBasis) Blended() Micros { return b.blended }

// UseYearMonth is the use-year-start-month heuristic: the first contract's
// (by ListContracts order, i.e. id), or January when there are none.
func (b CostBasis) UseYearMonth() time.Month { return b.useYearMonth }

// RateFor returns contractID's own priced rate, or the blended rate when
// contractID is nil, refers to a contract with no cost data, or refers to a
// contract CostBasis doesn't know about. It never returns a bare zero for a
// missing rate — the caller relies on Known() to know whether the number
// means anything.
func (b CostBasis) RateFor(contractID *int64) Micros {
	if contractID != nil {
		if rate, ok := b.rates[*contractID]; ok {
			return rate
		}
	}
	return b.blended
}

// PricePerPointYear derives this contract's amortised acquisition cost per
// point per year: (purchase price + closing costs) / (annual points × term
// years), in Micros. It is never stored — only ever computed from the
// stored inputs.
//
// ok is false ("cost unknown") when any of TermYears, PurchasePrice or
// ClosingCosts is zero (the pre-backfill state for every contract) or when
// AnnualPoints is zero, which would otherwise divide by zero.
func (c Contract) PricePerPointYear() (Micros, bool) {
	if c.TermYears <= 0 || c.PurchasePrice <= 0 || c.ClosingCosts <= 0 || c.AnnualPoints <= 0 {
		return 0, false
	}
	num := (int64(c.PurchasePrice) + int64(c.ClosingCosts)) * microsPerCent
	den := int64(c.AnnualPoints) * int64(c.TermYears)
	return Micros(divRound(num, den)), true
}
