package ledger

import (
	"context"
	"testing"
	"time"
)

func TestUseYearSummaries(t *testing.T) {
	s := openTestStore(t)
	add := func(year int, kind string, allotted, used int) {
		if _, err := s.AddEntry(context.Background(), Entry{UseYear: year, Date: date(t, "2026-04-01"), Kind: kind, Allotted: allotted, Used: used}); err != nil {
			t.Fatal(err)
		}
	}
	add(2025, KindAllocation, 120, 0)
	add(2025, KindUsage, 0, 200) // 2025 over-borrowed: net -80
	add(2026, KindAllocation, 120, 0)
	add(2026, KindAllocation, 150, 0)
	add(2026, KindSingleUse, 24, 0)
	add(2026, KindUsage, 0, 100) // 2026 net: 294 - 100 = 194

	got, err := s.UseYearSummaries(context.Background())
	if err != nil {
		t.Fatalf("UseYearSummaries: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	// Ordered ascending by use year.
	y2025, y2026 := got[0], got[1]
	if y2025.UseYear != 2025 || y2025.Allotted != 120 || y2025.Used != 200 || y2025.Net != -80 || !y2025.OverBorrowed {
		t.Errorf("2025 summary = %+v", y2025)
	}
	if y2026.UseYear != 2026 || y2026.Allotted != 294 || y2026.Used != 100 || y2026.Net != 194 || y2026.OverBorrowed {
		t.Errorf("2026 summary = %+v", y2026)
	}
}

func TestDistributeNextYear(t *testing.T) {
	s := openTestStore(t)
	cA, _ := s.AddContract(context.Background(), Contract{Name: "Point allocation", AnnualPoints: 120, UseYearMonth: time.April})
	cB, _ := s.AddContract(context.Background(), Contract{Name: "Point allocation #2", AnnualPoints: 150, UseYearMonth: time.April})

	// Seed each contract's latest allocation at use year 2026.
	mustAddAlloc(t, s, cA, 2026, 120)
	mustAddAlloc(t, s, cB, 2026, 150)

	created, err := s.distributeUpTo(context.Background(), 2027)
	if err != nil {
		t.Fatalf("distributeUpTo: %v", err)
	}
	if len(created) != 2 {
		t.Fatalf("created %d rows, want 2", len(created))
	}
	for _, e := range created {
		if e.UseYear != 2027 || e.Kind != KindAllocation {
			t.Errorf("created entry = %+v, want use year 2027 allocation", e)
		}
		if e.Date.Month() != time.April || e.Date.Year() != 2027 {
			t.Errorf("created date = %v, want April 2027", e.Date)
		}
	}

	// Idempotent: a second run with the same cap creates nothing (target 2028 > cap).
	created2, err := s.distributeUpTo(context.Background(), 2027)
	if err != nil {
		t.Fatalf("second distributeUpTo: %v", err)
	}
	if len(created2) != 0 {
		t.Fatalf("second run created %d rows, want 0", len(created2))
	}

	all, _ := s.ListEntries(context.Background())
	if len(all) != 4 {
		t.Fatalf("total entries = %d, want 4", len(all))
	}
}

func TestDistributeNextYearSkipsContractsWithoutAllocation(t *testing.T) {
	s := openTestStore(t)
	// Contract exists but has never posted an allocation — nothing to advance from.
	_, _ = s.AddContract(context.Background(), Contract{Name: "New contract", AnnualPoints: 100, UseYearMonth: time.June})
	created, err := s.distributeUpTo(context.Background(), 2027)
	if err != nil {
		t.Fatalf("distributeUpTo: %v", err)
	}
	if len(created) != 0 {
		t.Fatalf("created %d rows, want 0 (no prior allocation)", len(created))
	}
}

func mustAddAlloc(t *testing.T, s *Store, contractID int64, year, points int) {
	t.Helper()
	id := contractID
	if _, err := s.AddEntry(context.Background(), Entry{
		UseYear:    year,
		Date:       date(t, "2026-04-01"),
		Desc:       "Point allocation",
		Kind:       KindAllocation,
		Allotted:   points,
		ContractID: &id,
	}); err != nil {
		t.Fatal(err)
	}
}
