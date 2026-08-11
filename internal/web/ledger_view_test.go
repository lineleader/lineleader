package web

import (
	"testing"

	"github.com/lineleader/lineleader/internal/ledger"
)

// addEntries inserts n usage entries dated sequentially from 2026-01-01, one
// per day, and returns their (Allotted, Used) in insertion order so callers
// can compute the expected delta/date for each.
func addEntries(t *testing.T, store *ledger.Store, n int) {
	t.Helper()
	for i := range n {
		date := dateParse(t, "2026-01-01").AddDate(0, 0, i)
		_, err := store.AddEntry(ledger.Entry{
			UseYear:  2026,
			Date:     date,
			Desc:     "Entry",
			Kind:     ledger.KindUsage,
			Allotted: i, // varies per row so callers can distinguish rows
			Used:     0,
		})
		if err != nil {
			t.Fatalf("AddEntry(%d): %v", i, err)
		}
	}
}

func TestRecentEntriesLength(t *testing.T) {
	cases := []struct {
		name  string
		total int
		want  int
	}{
		{"fewer than 20", 5, 5},
		{"exactly 20", 20, 20},
		{"more than 20", 27, 20},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store := ledger.OpenTest(t)
			addEntries(t, store, c.total)
			h := &ledgerHandlers{store: store}

			view, err := h.buildLedgerView(0, 0, "")
			if err != nil {
				t.Fatal(err)
			}
			if len(view.Recent) != c.want {
				t.Errorf("len(Recent) = %d, want %d", len(view.Recent), c.want)
			}
		})
	}
}

func TestRecentEntriesReverseChronologicalAndEntriesUntouched(t *testing.T) {
	store := ledger.OpenTest(t)
	addEntries(t, store, 25)
	h := &ledgerHandlers{store: store}

	view, err := h.buildLedgerView(0, 0, "")
	if err != nil {
		t.Fatal(err)
	}

	// Entries stays ascending by (Date, ID): the History view still depends on it.
	for i := 1; i < len(view.Entries); i++ {
		if view.Entries[i].Date.Before(view.Entries[i-1].Date) {
			t.Fatalf("Entries not ascending at index %d", i)
		}
	}
	if len(view.Entries) != 25 {
		t.Fatalf("len(Entries) = %d, want 25", len(view.Entries))
	}

	// Recent is the newest 20, newest first: the last-inserted entry (Allotted
	// == 24, the highest, since addEntries sets Allotted = i) comes first.
	if len(view.Recent) != 20 {
		t.Fatalf("len(Recent) = %d, want 20", len(view.Recent))
	}
	for i := 1; i < len(view.Recent); i++ {
		prevDate := view.Recent[i-1].DateLabel
		curDate := view.Recent[i].DateLabel
		if curDate > prevDate {
			t.Errorf("Recent not reverse-chronological at index %d: %s came after %s", i, curDate, prevDate)
		}
	}
	// The newest entry was inserted last (day offset 24 -> 2026-01-25 -> "2026-01-25").
	if got, want := view.Recent[0].DateLabel, "2026-01-25"; got != want {
		t.Errorf("Recent[0].DateLabel = %q, want %q", got, want)
	}
	// The oldest of the retained 20 is day offset 5 (2026-01-06), since the 20
	// newest of 25 entries drop the oldest 5 (offsets 0-4).
	if got, want := view.Recent[19].DateLabel, "2026-01-06"; got != want {
		t.Errorf("Recent[19].DateLabel = %q, want %q", got, want)
	}
}

func TestRecentEntryDeltaLabel(t *testing.T) {
	cases := []struct {
		name     string
		allotted int
		used     int
		want     string
	}{
		{"positive", 150, 0, "+150"},
		{"negative", 0, 81, "-81"},
		{"zero", 50, 50, "+0"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store := ledger.OpenTest(t)
			_, err := store.AddEntry(ledger.Entry{
				UseYear: 2026, Date: dateParse(t, "2026-01-01"), Desc: "Row",
				Kind: ledger.KindUsage, Allotted: c.allotted, Used: c.used,
			})
			if err != nil {
				t.Fatal(err)
			}
			h := &ledgerHandlers{store: store}

			view, err := h.buildLedgerView(0, 0, "")
			if err != nil {
				t.Fatal(err)
			}
			if len(view.Recent) != 1 {
				t.Fatalf("len(Recent) = %d, want 1", len(view.Recent))
			}
			if got := view.Recent[0].DeltaLabel; got != c.want {
				t.Errorf("DeltaLabel = %q, want %q", got, c.want)
			}
			if want := c.allotted - c.used; view.Recent[0].Delta != want {
				t.Errorf("Delta = %d, want %d", view.Recent[0].Delta, want)
			}
		})
	}
}

func TestRecentEntryDateAndDesc(t *testing.T) {
	store := ledger.OpenTest(t)
	_, err := store.AddEntry(ledger.Entry{
		UseYear: 2026, Date: dateParse(t, "2026-04-07"), Desc: "Beach Club trip",
		Kind: ledger.KindUsage, Used: 40,
	})
	if err != nil {
		t.Fatal(err)
	}
	h := &ledgerHandlers{store: store}

	view, err := h.buildLedgerView(0, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Recent) != 1 {
		t.Fatalf("len(Recent) = %d, want 1", len(view.Recent))
	}
	row := view.Recent[0]
	if row.DateLabel != "2026-04-07" {
		t.Errorf("DateLabel = %q, want %q", row.DateLabel, "2026-04-07")
	}
	if row.Desc != "Beach Club trip" {
		t.Errorf("Desc = %q, want %q", row.Desc, "Beach Club trip")
	}
}

func TestSpentByYear(t *testing.T) {
	cases := []struct {
		name  string
		years []int // use years to create one usage entry each in
		want  []int // expected UseYear order in SpentByYear (newest first)
	}{
		{"fewer than 3", []int{2024, 2025}, []int{2025, 2024}},
		{"exactly 3", []int{2023, 2024, 2025}, []int{2025, 2024, 2023}},
		{"more than 3 picks newest 3", []int{2021, 2022, 2023, 2024, 2025}, []int{2025, 2024, 2023}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store := ledger.OpenTest(t)
			for _, y := range c.years {
				_, err := store.AddEntry(ledger.Entry{
					UseYear: y, Date: dateParse(t, "2026-01-01"), Desc: "Alloc",
					Kind: ledger.KindAllocation, Allotted: 100,
				})
				if err != nil {
					t.Fatal(err)
				}
				_, err = store.AddEntry(ledger.Entry{
					UseYear: y, Date: dateParse(t, "2026-01-02"), Desc: "Trip",
					Kind: ledger.KindUsage, Used: y - 2000, // distinct Used per year, != Net
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			h := &ledgerHandlers{store: store}

			view, err := h.buildLedgerView(0, 0, "")
			if err != nil {
				t.Fatal(err)
			}
			if len(view.SpentByYear) != len(c.want) {
				t.Fatalf("len(SpentByYear) = %d, want %d", len(view.SpentByYear), len(c.want))
			}
			for i, wantYear := range c.want {
				got := view.SpentByYear[i]
				if got.UseYear != wantYear {
					t.Errorf("SpentByYear[%d].UseYear = %d, want %d", i, got.UseYear, wantYear)
				}
				wantSpent := wantYear - 2000 // Used, not Net (Net would be 100 - Used)
				if got.Spent != wantSpent {
					t.Errorf("SpentByYear[%d].Spent = %d, want %d (Used, not Net)", i, got.Spent, wantSpent)
				}
			}
		})
	}
}

func TestBuildLedgerViewTotalUsesLastRunningBalance(t *testing.T) {
	store := ledger.OpenTest(t)
	if _, err := store.AddEntry(ledger.Entry{
		UseYear: 2026, Date: dateParse(t, "2026-01-01"), Desc: "Alloc",
		Kind: ledger.KindAllocation, Allotted: 100,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddEntry(ledger.Entry{
		UseYear: 2026, Date: dateParse(t, "2026-01-02"), Desc: "Trip",
		Kind: ledger.KindUsage, Used: 30,
	}); err != nil {
		t.Fatal(err)
	}
	h := &ledgerHandlers{store: store}

	view, err := h.buildLedgerView(0, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	entries, err := store.ListEntries()
	if err != nil {
		t.Fatal(err)
	}
	want := entries[len(entries)-1].RunningBalance
	if view.Total != want {
		t.Errorf("Total = %d, want %d (last ascending entry's RunningBalance)", view.Total, want)
	}
}
