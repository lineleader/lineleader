package web

import (
	"context"
	"fmt"
	"time"

	"github.com/lineleader/lineleader/internal/ledger"
)

// ledgerView is the data for the /ledger page and its #ledger-body fragment.
type ledgerView struct {
	Entries   []ledger.Entry
	Total     int
	Summaries []ledger.UseYearSummary
	Contracts []ledger.Contract
	Kinds     []string
	EditID    int64  // when non-zero, that entry row renders as an edit form
	Err       string // surfaced from a failed mutation

	// EditContractID is the contract-editing analogue of EditID: when
	// non-zero, that contract's row in the Contracts table renders as an
	// edit form (contract_edit_row) instead of its normal row.
	EditContractID int64
	Recent         []recentEntryRow
	SpentByYear    []yearSpend

	// ShowCosts is the single gate every dollar affordance on the ledger
	// pages is wrapped in: true only when ledger.CostBasis.Known(), i.e. at
	// least one contract has priced cost data AND at least one dues rate is
	// stored. Before the owner backfills a contract's price/closing/term,
	// this is false and the ledger pages render with no dollar sign
	// anywhere — byte-identical in spirit to the page before this feature
	// existed (see TestLedgerHistoryHidesCostsWhenUnknown).
	ShowCosts bool

	// TotalCostLabel is the formatted sum of every priced entry's Cost
	// (Used-point rows with CostKnown), for the ledger grid's "Total spent"
	// <tfoot>. Meaningless (and not rendered) unless ShowCosts.
	TotalCostLabel string

	// CostFootnote is true when any entry (equivalently, any per-use-year
	// summary — PriceSummaries only sets CostProjected from a contributing
	// entry) shown on the History view used a projected dues rate, driving
	// the single shared footnote explaining the "*" marker.
	CostFootnote bool

	// BalanceValueLabel is the Recent view's "≈ $X" valuation of the
	// current balance, priced at the blended rate (nil contract id — the
	// balance is pooled across every contract, so no single contract's rate
	// applies) and the current use year's dues. See balanceValueLabel for
	// how it handles a negative (borrowed) balance. Meaningless (and not
	// rendered) unless ShowCosts.
	BalanceValueLabel string

	// BlendedRateLabel is the portfolio-wide $/pt/yr rate (the
	// annual-points-weighted mean of every priced contract's own rate),
	// shown below the Contracts table. Meaningless (and not rendered)
	// unless ShowCosts.
	BlendedRateLabel string

	// ContractRows is every contract, pre-formatted for the Contracts view
	// — see contractRow. Unlike the cost fields above, it is always fully
	// populated regardless of ShowCosts: its PriceInput/ClosingInput fields
	// feed the (always-available) contract edit form, which is how the
	// owner backfills a contract's cost data in the first place. Only the
	// template's *display* of the derived cost columns is gated on
	// ShowCosts.
	ContractRows []contractRow

	// Dues is every stored dues rate ascending by use year, plus
	// duesPreviewYears read-only projected rows beyond the last stored
	// year — see duesRows. Rendered (with the rest of the Contracts view's
	// new dues-management section) only under ShowCosts: dues management
	// isn't useful before the owner has backfilled at least one contract,
	// since nothing can be priced yet regardless of how many dues years are
	// on file.
	Dues []duesRow
}

// recentActivityLimit caps the Recent Activity list to the newest entries.
const recentActivityLimit = 20

// recentEntryRow is one row of the Recent Activity list: a single entry
// pre-formatted for display (newest first), with the signed points delta
// computed and rendered once here so the template stays dumb.
type recentEntryRow struct {
	DateLabel  string // ledger.DateLayout, e.g. "2026-04-07" — full ISO date, since the list spans multiple years
	Desc       string
	Delta      int    // Allotted - Used
	DeltaLabel string // Delta with an explicit sign, e.g. "+150" / "-81" / "+0"

	// CostLabel is the formatted dollar cost of this entry's Used points
	// ("" when CostKnown is false — an allocation row, or CostBasis not
	// Known()). CostProjected mirrors the underlying entry's flag. Only
	// rendered under the page's ShowCosts guard.
	CostLabel     string
	CostProjected bool
}

// yearSpend is one row of the Spent by Use Year widget: points SPENT in that
// use year (UseYearSummary.Used), not Net — a year can spend more than it
// received (borrowing) and this widget wants that raw spend number.
type yearSpend struct {
	UseYear int
	Spent   int

	// CostLabel/CostProjected mirror the matching UseYearSummary's Cost
	// fields (see recentEntryRow.CostLabel).
	CostLabel     string
	CostProjected bool
}

// contractRow is one row of the Contracts table, a contract pre-formatted
// for display and — via PriceInput/ClosingInput — for the edit form.
type contractRow struct {
	ID           int64
	Name         string
	Number       string
	HomeResort   string
	AnnualPoints int
	UseYearMonth time.Month
	TermYears    int

	// PriceLabel/ClosingLabel are FormatUSD(PurchasePrice/ClosingCosts) —
	// always a well-formed dollar string (Cents' zero value formats as
	// "$0.00", which is accurate: the stored value really is zero/unset).
	// Rendered only under the page's ShowCosts guard.
	PriceLabel   string
	ClosingLabel string

	// RateLabel is FormatRate(rate) when this contract's own
	// PricePerPointYear is known, "—" otherwise — a contract can be
	// individually unpriced even while ShowCosts is true portfolio-wide
	// (some other contract carries the rate).
	RateLabel string

	// PriceInput/ClosingInput are Cents.String() — round-trippable through
	// ledger.ParseCents — for the edit form's <input value="...">. These
	// are populated unconditionally (not gated on ShowCosts): editing is
	// how the owner enters a contract's cost data the first time.
	PriceInput   string
	ClosingInput string
}

// contractRows projects every contract into its display/edit row.
func contractRows(contracts []ledger.Contract) []contractRow {
	rows := make([]contractRow, len(contracts))
	for i, c := range contracts {
		rateLabel := "—"
		if rate, ok := c.PricePerPointYear(); ok {
			rateLabel = ledger.FormatRate(rate)
		}
		rows[i] = contractRow{
			ID:           c.ID,
			Name:         c.Name,
			Number:       c.Number,
			HomeResort:   c.HomeResort,
			AnnualPoints: c.AnnualPoints,
			UseYearMonth: c.UseYearMonth,
			TermYears:    c.TermYears,
			PriceLabel:   ledger.FormatUSD(c.PurchasePrice),
			ClosingLabel: ledger.FormatUSD(c.ClosingCosts),
			RateLabel:    rateLabel,
			PriceInput:   c.PurchasePrice.String(),
			ClosingInput: c.ClosingCosts.String(),
		}
	}
	return rows
}

// duesPreviewYears is how many projected years beyond the last stored dues
// rate the Contracts view's dues table shows, read-only.
const duesPreviewYears = 3

// duesRow is one row of the Contracts view's dues table: either a stored
// rate (Projected false, deletable) or one of the duesPreviewYears
// read-only projected rows following it.
type duesRow struct {
	UseYear   int
	RateLabel string
	Projected bool
}

// duesRows returns every stored dues rate ascending by use year, exactly as
// given (dues is already Store.ListDuesRates order), followed by
// duesPreviewYears projected rows continuing from the last stored year. When
// dues is empty there is no "last stored year" to project from, so no
// preview rows are added — an empty dues table, not a fabricated one.
func duesRows(basis ledger.CostBasis, dues []ledger.DuesRate) []duesRow {
	rows := make([]duesRow, 0, len(dues)+duesPreviewYears)
	for _, d := range dues {
		rows = append(rows, duesRow{UseYear: d.UseYear, RateLabel: ledger.FormatRate(d.Rate), Projected: false})
	}
	if len(dues) == 0 {
		return rows
	}
	lastYear := dues[len(dues)-1].UseYear
	for i := 1; i <= duesPreviewYears; i++ {
		year := lastYear + i
		rate, _ := basis.DuesFor(year)
		rows = append(rows, duesRow{UseYear: year, RateLabel: ledger.FormatRate(rate), Projected: true})
	}
	return rows
}

// costLabel formats cost as a dollar string, or "" when known is false —
// shared by recentEntries and spentByYear so a not-yet-priced row (an
// allocation, or any row when CostBasis isn't Known()) renders blank rather
// than "$0.00".
func costLabel(cost ledger.Cents, known bool) string {
	if !known {
		return ""
	}
	return ledger.FormatUSD(cost)
}

// entryKinds lists the selectable kinds for the add/edit form.
var entryKinds = []string{
	ledger.KindUsage,
	ledger.KindAllocation,
	ledger.KindBonus,
	ledger.KindSingleUse,
	ledger.KindAdjustment,
}

// buildLedgerView reads the current ledger state into a view. editID marks
// an entry row for inline editing; editContractID marks a contract row the
// same way; errMsg surfaces a mutation error. Caller holds the lock.
func (h *ledgerHandlers) buildLedgerView(ctx context.Context, editID, editContractID int64, errMsg string) (ledgerView, error) {
	entries, err := h.store.ListEntries(ctx)
	if err != nil {
		return ledgerView{}, err
	}
	summaries, err := h.store.UseYearSummaries(ctx)
	if err != nil {
		return ledgerView{}, err
	}
	contracts, err := h.store.ListContracts(ctx)
	if err != nil {
		return ledgerView{}, err
	}
	dues, err := h.store.ListDuesRates(ctx)
	if err != nil {
		return ledgerView{}, err
	}
	total := 0
	if n := len(entries); n > 0 {
		total = entries[n-1].RunningBalance
	}

	basis, err := h.store.CostBasis(ctx)
	if err != nil {
		return ledgerView{}, err
	}
	basis.PriceEntries(entries)
	ledger.PriceSummaries(summaries, entries)
	totalCost, costFootnote := sumEntryCosts(entries)

	return ledgerView{
		Entries:           entries,
		Total:             total,
		Summaries:         summaries,
		Contracts:         contracts,
		Kinds:             entryKinds,
		EditID:            editID,
		EditContractID:    editContractID,
		Err:               errMsg,
		Recent:            recentEntries(entries),
		SpentByYear:       spentByYear(summaries, ledger.UseYearForDate(time.Now(), basis.UseYearMonth())),
		ShowCosts:         basis.Known(),
		TotalCostLabel:    ledger.FormatUSD(totalCost),
		CostFootnote:      costFootnote,
		BalanceValueLabel: balanceValueLabel(basis, total),
		BlendedRateLabel:  ledger.FormatRate(basis.Blended()),
		ContractRows:      contractRows(contracts),
		Dues:              duesRows(basis, dues),
	}, nil
}

// balanceValueLabel formats the Recent view's "≈ $X" approximation of the
// current balance's dollar value: balance points priced at the blended rate
// (nil contract id — the balance is pooled across every contract, so no
// single contract's own rate is more correct than another's) and the
// current use year's dues (see ledger.UseYearForDate).
//
// It deliberately does NOT go through CostBasis.Cost: that method treats
// any non-positive point count as "unpriceable" (correct for a single
// entry's Used, which is never negative), but a balance can legitimately be
// zero or negative ("borrowed" — the owner has drawn down more points than
// they've been allotted). PointCost multiplies directly and handles a
// negative balance by producing a negative dollar figure — read as "you've
// borrowed the equivalent of $X of not-yet-allotted points" — which
// FormatUSD renders with a leading "-", e.g. "≈ -$123.45".
func balanceValueLabel(basis ledger.CostBasis, balance int) string {
	if !basis.Known() {
		return ""
	}
	year := ledger.UseYearForDate(time.Now(), basis.UseYearMonth())
	dues, _ := basis.DuesFor(year)
	cost := ledger.PointCost(balance, basis.RateFor(nil), dues)
	return ledger.FormatUSD(cost)
}

// sumEntryCosts totals every priced entry's Cost (CostKnown entries only —
// see CostBasis.PriceEntries) and reports whether any of them used a
// projected dues rate.
func sumEntryCosts(entries []ledger.Entry) (total ledger.Cents, anyProjected bool) {
	for _, e := range entries {
		if !e.CostKnown {
			continue
		}
		total += e.Cost
		if e.CostProjected {
			anyProjected = true
		}
	}
	return total, anyProjected
}

// recentEntries returns the newest recentActivityLimit entries (or fewer, if
// entries has fewer than that many) in reverse-chronological order. entries
// is ascending by (Date, ID) — the same slice the History view renders — so
// this walks it backwards into a fresh slice rather than sorting in place,
// which would reorder Entries out from under the History view.
func recentEntries(entries []ledger.Entry) []recentEntryRow {
	n := len(entries)
	if n > recentActivityLimit {
		n = recentActivityLimit
	}
	rows := make([]recentEntryRow, n)
	for i := range n {
		e := entries[len(entries)-1-i] // newest first: walk back from the end
		delta := e.Allotted - e.Used
		rows[i] = recentEntryRow{
			DateLabel:     e.Date.Format(ledger.DateLayout),
			Desc:          e.Desc,
			Delta:         delta,
			DeltaLabel:    formatSignedDelta(delta),
			CostLabel:     costLabel(e.Cost, e.CostKnown),
			CostProjected: e.CostProjected,
		}
	}
	return rows
}

// formatSignedDelta renders a points delta with an explicit sign, e.g.
// "+150", "-81", "+0".
func formatSignedDelta(delta int) string {
	if delta < 0 {
		return fmt.Sprintf("-%d", -delta)
	}
	return fmt.Sprintf("+%d", delta)
}

// spentByYear returns the spentByYearLimit most recent use years (newest
// first), each reporting points spent (UseYearSummary.Used). summaries is
// ascending by UseYear, as returned by Store.UseYearSummaries.
//
// A future use year (UseYear > currentUseYear) with no spend is dropped
// before the limit is applied: an allocation entry creates a zero-Used
// summary for every use year it seeds in advance, so without this filter
// the widget would fill up with empty years-not-yet-arrived and push
// actual spending history off the bottom. A future year that DOES have
// spend (a trip booked ahead against banked/borrowed points) is kept, and
// a past or current year is always kept even at zero spend — those are
// real, already-elapsed history, not placeholders.
func spentByYear(summaries []ledger.UseYearSummary, currentUseYear int) []yearSpend {
	kept := make([]ledger.UseYearSummary, 0, len(summaries))
	for _, s := range summaries {
		if s.UseYear > currentUseYear && s.Used == 0 {
			continue
		}
		kept = append(kept, s)
	}

	n := len(kept)
	start := 0
	if n > spentByYearLimit {
		start = n - spentByYearLimit
	}
	out := make([]yearSpend, 0, n-start)
	for i := n - 1; i >= start; i-- {
		s := kept[i]
		out = append(out, yearSpend{
			UseYear:       s.UseYear,
			Spent:         s.Used,
			CostLabel:     costLabel(s.Cost, s.CostKnown),
			CostProjected: s.CostProjected,
		})
	}
	return out
}

// spentByYearLimit caps the Spent by Use Year widget to the most recent
// years (after the future-year filter in spentByYear runs, so hidden empty
// future years never consume a slot that real spending history could use).
const spentByYearLimit = 5
