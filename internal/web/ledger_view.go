package web

import "github.com/lineleader/lineleader/internal/ledger"

// ledgerView is the data for the /ledger page and its #ledger-body fragment.
type ledgerView struct {
	Entries   []ledger.Entry
	Total     int
	Summaries []ledger.UseYearSummary
	Contracts []ledger.Contract
	Kinds     []string
	EditID    int64  // when non-zero, that entry row renders as an edit form
	Err       string // surfaced from a failed mutation
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
		Entries:   entries,
		Total:     total,
		Summaries: summaries,
		Contracts: contracts,
		Kinds:     entryKinds,
		EditID:    editID,
		Err:       errMsg,
	}, nil
}
