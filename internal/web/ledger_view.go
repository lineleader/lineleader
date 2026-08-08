package web

import (
	"fmt"

	"github.com/lineleader/lineleader/internal/ledger"
)

// ledgerView is the data for the /ledger page and its #ledger-body fragment.
type ledgerView struct {
	Entries     []ledger.Entry
	Total       int
	Summaries   []ledger.UseYearSummary
	Contracts   []ledger.Contract
	Kinds       []string
	EditID      int64  // when non-zero, that entry row renders as an edit form
	Err         string // surfaced from a failed mutation
	Recent      []recentEntryRow
	SpentByYear []yearSpend
}

// recentActivityLimit caps the Recent Activity list to the newest entries.
const recentActivityLimit = 20

// recentEntryRow is one row of the Recent Activity list: a single entry
// pre-formatted for display (newest first), with the signed points delta
// computed and rendered once here so the template stays dumb.
type recentEntryRow struct {
	DateLabel  string // "MM-DD", e.g. "04-07"
	Desc       string
	Delta      int    // Allotted - Used
	DeltaLabel string // Delta with an explicit sign, e.g. "+150" / "-81" / "+0"
}

// yearSpend is one row of the Spent by Use Year widget: points SPENT in that
// use year (UseYearSummary.Used), not Net — a year can spend more than it
// received (borrowing) and this widget wants that raw spend number.
type yearSpend struct {
	UseYear int
	Spent   int
}

// entryKinds lists the selectable kinds for the add/edit form.
var entryKinds = []string{
	ledger.KindUsage,
	ledger.KindAllocation,
	ledger.KindBonus,
	ledger.KindSingleUse,
	ledger.KindAdjustment,
}

// buildLedgerView reads the current ledger state into a view. editID marks a row
// for inline editing; errMsg surfaces a mutation error. Caller holds the lock.
func (h *ledgerHandlers) buildLedgerView(editID int64, errMsg string) (ledgerView, error) {
	entries, err := h.store.ListEntries()
	if err != nil {
		return ledgerView{}, err
	}
	summaries, err := h.store.UseYearSummaries()
	if err != nil {
		return ledgerView{}, err
	}
	contracts, err := h.store.ListContracts()
	if err != nil {
		return ledgerView{}, err
	}
	total := 0
	if n := len(entries); n > 0 {
		total = entries[n-1].RunningBalance
	}
	return ledgerView{
		Entries:     entries,
		Total:       total,
		Summaries:   summaries,
		Contracts:   contracts,
		Kinds:       entryKinds,
		EditID:      editID,
		Err:         errMsg,
		Recent:      recentEntries(entries),
		SpentByYear: spentByYear(summaries),
	}, nil
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
			DateLabel:  e.Date.Format("01-02"),
			Desc:       e.Desc,
			Delta:      delta,
			DeltaLabel: formatSignedDelta(delta),
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
func spentByYear(summaries []ledger.UseYearSummary) []yearSpend {
	n := len(summaries)
	start := 0
	if n > spentByYearLimit {
		start = n - spentByYearLimit
	}
	out := make([]yearSpend, 0, n-start)
	for i := n - 1; i >= start; i-- {
		out = append(out, yearSpend{UseYear: summaries[i].UseYear, Spent: summaries[i].Used})
	}
	return out
}

// spentByYearLimit caps the Spent by Use Year widget to the most recent years.
const spentByYearLimit = 3
