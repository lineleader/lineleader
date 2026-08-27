package ledger

import (
	"context"
	"sort"
	"time"
)

// duesGrowthBase is the fixed-point scale (1e6) used for growthMicros and
// every dues-projection step: an integer ratio rather than a float, so no
// float ever enters this package.
const duesGrowthBase = 1_000_000

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
		dues:         make(map[int]Micros),
		useYearMonth: UseYearStartMonth(contracts),
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

	for _, d := range dues {
		if d.Rate <= 0 {
			// A non-positive dues rate cannot happen via UpsertDuesRate
			// (which now rejects it), but tolerate it defensively anyway —
			// some other path (direct SQL, a future API) could still insert
			// one, and letting it into the map would later reach
			// duesGrowth's or compoundDues' division as either operand and
			// panic. Treat that year exactly as if it were absent.
			continue
		}
		b.dues[d.UseYear] = d.Rate
	}
	if len(b.dues) > 0 {
		b.hasDues = true
		b.firstYear, b.lastYear, b.growthMicros = duesGrowth(b.dues)
	}

	return b
}

// duesGrowth computes the [firstYear, lastYear] span of a dues map and its
// growthMicros: the mean year-over-year ratio (scaled by duesGrowthBase)
// across every adjacent pair of stored years exactly one year apart. Pairs
// spanning more than one year are skipped (they would bias the mean); no
// such pair at all (0 or 1 stored years) means growthMicros is flat
// (duesGrowthBase, i.e. 1.0).
func duesGrowth(dues map[int]Micros) (firstYear, lastYear int, growthMicros int64) {
	years := make([]int, 0, len(dues))
	for y := range dues {
		years = append(years, y)
	}
	sort.Ints(years)
	firstYear, lastYear = years[0], years[len(years)-1]

	var sumRatio, count int64
	for i := 0; i+1 < len(years); i++ {
		y, yNext := years[i], years[i+1]
		if yNext-y != 1 {
			continue
		}
		sumRatio += divRound(int64(dues[yNext])*duesGrowthBase, int64(dues[y]))
		count++
	}
	if count == 0 {
		growthMicros = duesGrowthBase
	} else {
		growthMicros = divRound(sumRatio, count)
	}
	return firstYear, lastYear, growthMicros
}

// Known reports whether CostBasis has enough data to price anything at
// all: at least one priced contract (for a rate) and at least one stored
// dues rate.
func (b CostBasis) Known() bool { return b.hasRate && b.hasDues }

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

// DuesFor returns the $/pt dues rate for year. A stored year returns its
// exact rate with projected=false. Otherwise the rate is derived by
// compounding growthMicros one year at a time — never by a single
// exponentiation — so the math stays integer and deterministic:
//
//   - year > lastYear: compounded forward from dues[lastYear]
//   - year < firstYear: compounded backward (divided) from dues[firstYear]
//   - an interior gap: compounded forward from the nearest stored year below
//
// and projected is true in all three cases. There is no cap on projection
// distance — projected=true is the honesty mechanism, not a limit. When
// CostBasis has no dues data at all, DuesFor returns (0, false); callers
// should gate on Known() first, as Cost does.
func (b CostBasis) DuesFor(year int) (rate Micros, projected bool) {
	if rate, ok := b.dues[year]; ok {
		return rate, false
	}
	if !b.hasDues {
		return 0, false
	}

	switch {
	case year > b.lastYear:
		return b.compoundDues(b.lastYear, year-b.lastYear), true
	case year < b.firstYear:
		return b.compoundDues(b.firstYear, year-b.firstYear), true
	default:
		below := b.nearestDuesYearBelow(year)
		return b.compoundDues(below, year-below), true
	}
}

// compoundDues compounds dues[baseYear] forward steps years (steps may be
// negative, meaning "divide backward" that many years), one year at a time.
//
// growthMicros should always be positive in practice — NewCostBasis only
// ever derives it from positive stored dues rates, defaulting to
// duesGrowthBase (never 0) when there's nothing to compute a ratio from —
// but this is belt and braces: if some future change ever left it
// non-positive, dividing by it below (the backward branch) would panic.
// Returning the base rate unchanged is the same honesty trade-off DuesFor
// already makes for flat growth: a defensible answer, still flagged
// projected by the caller, rather than a crash.
func (b CostBasis) compoundDues(baseYear, steps int) Micros {
	rate := int64(b.dues[baseYear])
	if b.growthMicros <= 0 {
		return Micros(rate)
	}
	for range steps {
		rate = divRound(rate*b.growthMicros, duesGrowthBase)
	}
	for range -steps {
		rate = divRound(rate*duesGrowthBase, b.growthMicros)
	}
	return Micros(rate)
}

// nearestDuesYearBelow finds the closest stored year strictly less than
// year, for pricing an interior gap. year is always within [firstYear,
// lastYear] when this is called, so firstYear (always stored) is reached
// in the worst case.
func (b CostBasis) nearestDuesYearBelow(year int) int {
	for y := year - 1; y >= b.firstYear; y-- {
		if _, ok := b.dues[y]; ok {
			return y
		}
	}
	return b.firstYear
}

// Cost prices points points used in use year year against contractID's rate
// (or the blended rate when contractID is nil or unpriced — see RateFor)
// and year's dues rate (stored or projected — see DuesFor).
//
// known is false — and cost is 0, projected is meaningless — when CostBasis
// isn't Known() at all or points <= 0 (an allocation, or any non-positive
// row, is never priced). Otherwise known is true and projected reports
// whether year's dues rate was projected rather than stored.
func (b CostBasis) Cost(points, year int, contractID *int64) (cost Cents, projected bool, known bool) {
	if !b.Known() || points <= 0 {
		return 0, false, false
	}
	dues, duesProjected := b.DuesFor(year)
	return PointCost(points, b.RateFor(contractID), dues), duesProjected, true
}

// PriceEntries prices every entry's Used points in place — Cost, CostKnown
// and CostProjected are set from b.Cost(e.Used, e.UseYear, e.ContractID) —
// the same style as ListEntries deriving RunningBalance. Allotted is never
// priced: an allocation row's Used is always 0, so Cost stays 0 and
// CostKnown stays false for it.
func (b CostBasis) PriceEntries(entries []Entry) {
	for i := range entries {
		e := &entries[i]
		e.Cost, e.CostProjected, e.CostKnown = b.Cost(e.Used, e.UseYear, e.ContractID)
	}
}

// PriceSummaries aggregates every already-priced entry's Cost (see
// PriceEntries) into the matching UseYearSummary by UseYear, in place. Only
// entries with CostKnown contribute; a use year with no priced entries (no
// usage that year, or CostBasis not Known()) is left at its zero value
// (CostKnown false, Cost 0). CostProjected is set on a summary if any
// contributing entry's dues rate was projected.
func PriceSummaries(summaries []UseYearSummary, priced []Entry) {
	index := make(map[int]int, len(summaries))
	for i, s := range summaries {
		index[s.UseYear] = i
	}
	for _, e := range priced {
		if !e.CostKnown {
			continue
		}
		i, ok := index[e.UseYear]
		if !ok {
			continue
		}
		summaries[i].Cost += e.Cost
		summaries[i].CostKnown = true
		if e.CostProjected {
			summaries[i].CostProjected = true
		}
	}
}

// CostBasis derives the current CostBasis from the store's contracts and
// dues rates — the only I/O anywhere in the cost model.
func (s *Store) CostBasis(ctx context.Context) (CostBasis, error) {
	contracts, err := s.ListContracts(ctx)
	if err != nil {
		return CostBasis{}, err
	}
	dues, err := s.ListDuesRates(ctx)
	if err != nil {
		return CostBasis{}, err
	}
	return NewCostBasis(contracts, dues), nil
}

// PricePerPointYear derives this contract's amortised acquisition cost per
// point per year: (purchase price + closing costs) / (annual points × term
// years), in Micros. It is never stored — only ever computed from the
// stored inputs.
//
// ok is false ("cost unknown") when any of TermYears, PurchasePrice or
// AnnualPoints is zero or negative (the pre-backfill state for every
// contract, or a value that would otherwise divide by zero) or when
// ClosingCosts is negative. ClosingCosts == 0 is legitimate — waived, or
// rolled into the purchase price — and prices normally.
func (c Contract) PricePerPointYear() (Micros, bool) {
	if c.TermYears <= 0 || c.PurchasePrice <= 0 || c.ClosingCosts < 0 || c.AnnualPoints <= 0 {
		return 0, false
	}
	num := (int64(c.PurchasePrice) + int64(c.ClosingCosts)) * microsPerCent
	den := int64(c.AnnualPoints) * int64(c.TermYears)
	return Micros(divRound(num, den)), true
}
