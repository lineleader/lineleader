package ledger

import (
	"context"
	"testing"
	"time"
)

func date(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestEntryCRUD(t *testing.T) {
	s := openTestStore(t)

	e := Entry{
		UseYear:  2026,
		Date:     date(t, "2026-04-01"),
		Desc:     "Point allocation",
		Kind:     KindAllocation,
		Allotted: 120,
	}
	id, err := s.AddEntry(context.Background(), e)
	if err != nil {
		t.Fatalf("AddEntry: %v", err)
	}

	got, err := s.ListEntries(context.Background())
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].ID != id || got[0].Desc != "Point allocation" || got[0].Allotted != 120 {
		t.Errorf("round-trip mismatch: %+v", got[0])
	}
	if !got[0].Date.Equal(e.Date) {
		t.Errorf("date = %v, want %v", got[0].Date, e.Date)
	}

	// Edit it into a usage.
	upd := got[0]
	upd.Kind = KindUsage
	upd.Desc = "RIV 2br Std"
	upd.Allotted = 0
	upd.Used = 240
	upd.Tag = "Borrow"
	if err := s.UpdateEntry(context.Background(), upd); err != nil {
		t.Fatalf("UpdateEntry: %v", err)
	}
	got, _ = s.ListEntries(context.Background())
	if got[0].Used != 240 || got[0].Tag != "Borrow" || got[0].Kind != KindUsage {
		t.Errorf("after update: %+v", got[0])
	}

	if err := s.DeleteEntry(context.Background(), id); err != nil {
		t.Fatalf("DeleteEntry: %v", err)
	}
	got, _ = s.ListEntries(context.Background())
	if len(got) != 0 {
		t.Fatalf("after delete len = %d, want 0", len(got))
	}
}

func TestRunningBalance(t *testing.T) {
	s := openTestStore(t)

	// A sequence exercising allocation, usage, borrow-into-negative, restore, single-use.
	seq := []Entry{
		{UseYear: 2019, Date: date(t, "2019-11-01"), Desc: "Point allocation", Kind: KindAllocation, Allotted: 120},
		{UseYear: 2020, Date: date(t, "2020-04-01"), Desc: "Point allocation", Kind: KindAllocation, Allotted: 120},
		{UseYear: 2020, Date: date(t, "2020-05-01"), Desc: "GF 1BR", Kind: KindUsage, Used: 308, Tag: "Borrow"},
		{UseYear: 2020, Date: date(t, "2020-09-01"), Desc: "Point allocation #2", Kind: KindAllocation, Allotted: 150},
		{UseYear: 2021, Date: date(t, "2021-04-01"), Desc: "Single-use points", Kind: KindSingleUse, Allotted: 24},
	}
	for _, e := range seq {
		if _, err := s.AddEntry(context.Background(), e); err != nil {
			t.Fatalf("AddEntry: %v", err)
		}
	}

	got, err := s.ListEntries(context.Background())
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	want := []int{120, 240, -68, 82, 106} // running cumulative (allotted - used)
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].RunningBalance != want[i] {
			t.Errorf("row %d (%s) RunningBalance = %d, want %d", i, got[i].Desc, got[i].RunningBalance, want[i])
		}
	}
}

func TestListEntriesOrdersByDateThenID(t *testing.T) {
	s := openTestStore(t)
	// Insert out of date order; ListEntries must sort by (date, id).
	_, _ = s.AddEntry(context.Background(), Entry{UseYear: 2022, Date: date(t, "2022-09-17"), Desc: "later date", Kind: KindUsage, Used: 8})
	_, _ = s.AddEntry(context.Background(), Entry{UseYear: 2022, Date: date(t, "2022-07-17"), Desc: "earlier date", Kind: KindUsage, Used: 5})
	got, _ := s.ListEntries(context.Background())
	if got[0].Desc != "earlier date" || got[1].Desc != "later date" {
		t.Errorf("order = [%s, %s], want earlier first", got[0].Desc, got[1].Desc)
	}
}
