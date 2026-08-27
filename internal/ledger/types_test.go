package ledger

import (
	"testing"
	"time"
)

// TestUseYearStartMonth pins the use-year-start-month heuristic: the first
// contract's UseYearMonth (ListContracts order, i.e. by id), or January
// when there are no contracts at all. Several contracts with different
// months must resolve to the FIRST one's — that's the documented
// single-portfolio-use-year assumption, not a bug to fix here.
func TestUseYearStartMonth(t *testing.T) {
	t.Run("no contracts", func(t *testing.T) {
		if got := UseYearStartMonth(nil); got != time.January {
			t.Errorf("UseYearStartMonth(nil) = %v, want %v", got, time.January)
		}
	})

	t.Run("one contract", func(t *testing.T) {
		aug := Contract{ID: 5, UseYearMonth: time.August}
		if got := UseYearStartMonth([]Contract{aug}); got != time.August {
			t.Errorf("UseYearStartMonth() = %v, want %v", got, time.August)
		}
	})

	t.Run("several contracts, different months, first wins", func(t *testing.T) {
		aug := Contract{ID: 5, UseYearMonth: time.August}
		apr := Contract{ID: 1, UseYearMonth: time.April}
		if got := UseYearStartMonth([]Contract{aug, apr}); got != time.August {
			t.Errorf("UseYearStartMonth() = %v, want %v (first contract's)", got, time.August)
		}
	})
}
