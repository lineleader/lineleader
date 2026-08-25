package ledger

import (
	"context"
	"testing"
)

// TestStoreCostBasis wires Store.CostBasis (the one I/O boundary of the cost
// model) to a real database: a priced contract plus the auto-seeded dues
// series should yield a Known() basis with the contract's own rate.
func TestStoreCostBasis(t *testing.T) {
	s := openTestStore(t)

	id, err := s.AddContract(context.Background(), Contract{
		Name: "Point allocation", AnnualPoints: 120, UseYearMonth: 4,
		TermYears: 44, PurchasePrice: 2_940_000, ClosingCosts: 58_835,
	})
	if err != nil {
		t.Fatalf("AddContract: %v", err)
	}

	basis, err := s.CostBasis(context.Background())
	if err != nil {
		t.Fatalf("CostBasis: %v", err)
	}
	if !basis.Known() {
		t.Fatal("CostBasis().Known() = false, want true (contract priced, dues seeded)")
	}
	if got := basis.RateFor(&id); got != 5_679_612 {
		t.Errorf("RateFor(contract) = %d, want 5_679_612", got)
	}
}

// TestPriceEntries exercises the full path from stored entries through
// Store.CostBasis and CostBasis.PriceEntries: a usage entry against its own
// contract's rate, a usage entry with no contract falling back to blended,
// an allocation entry (Used == 0) staying cost-unknown, and a usage entry
// in a projected dues year carrying CostProjected.
func TestPriceEntries(t *testing.T) {
	s := openTestStore(t)

	id2019, err := s.AddContract(context.Background(), Contract{
		Name: "Point allocation", AnnualPoints: 120, UseYearMonth: 4,
		TermYears: 44, PurchasePrice: 2_940_000, ClosingCosts: 58_835,
	})
	if err != nil {
		t.Fatalf("AddContract: %v", err)
	}
	if _, err := s.AddContract(context.Background(), Contract{
		Name: "Point allocation #2", AnnualPoints: 150, UseYearMonth: 4,
		TermYears: 41, PurchasePrice: 3_015_000, ClosingCosts: 66_500,
	}); err != nil {
		t.Fatalf("AddContract: %v", err)
	}

	entries := []Entry{
		{UseYear: 2025, Date: date(t, "2025-05-01"), Desc: "allocation", Kind: KindAllocation, Allotted: 120},
		{UseYear: 2025, Date: date(t, "2025-06-01"), Desc: "own contract", Kind: KindUsage, Used: 100, ContractID: &id2019},
		{UseYear: 2021, Date: date(t, "2021-06-01"), Desc: "blended fallback", Kind: KindUsage, Used: 240},
		{UseYear: 2027, Date: date(t, "2027-06-01"), Desc: "projected dues", Kind: KindUsage, Used: 50},
	}
	for _, e := range entries {
		if _, err := s.AddEntry(context.Background(), e); err != nil {
			t.Fatalf("AddEntry(%s): %v", e.Desc, err)
		}
	}

	basis, err := s.CostBasis(context.Background())
	if err != nil {
		t.Fatalf("CostBasis: %v", err)
	}
	got, err := s.ListEntries(context.Background())
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	basis.PriceEntries(got)

	byDesc := make(map[string]Entry, len(got))
	for _, e := range got {
		byDesc[e.Desc] = e
	}

	if e := byDesc["allocation"]; e.CostKnown || e.Cost != 0 {
		t.Errorf("allocation entry = %+v, want CostKnown=false Cost=0", e)
	}
	if e := byDesc["own contract"]; !e.CostKnown || e.CostProjected || e.Cost != 136_094 {
		t.Errorf("own-contract entry = %+v, want Cost=136_094 CostKnown=true CostProjected=false", e)
	}
	if e := byDesc["blended fallback"]; !e.CostKnown || e.CostProjected || e.Cost != 290_873 {
		t.Errorf("blended-fallback entry = %+v, want Cost=290_873 CostKnown=true CostProjected=false", e)
	}
	if e := byDesc["projected dues"]; !e.CostKnown || !e.CostProjected || e.Cost == 0 {
		t.Errorf("projected-dues entry = %+v, want CostKnown=true CostProjected=true Cost!=0", e)
	}
}

// TestPriceSummaries checks that PriceSummaries aggregates each use year's
// known-cost entries (and only those) into the matching UseYearSummary,
// propagating CostProjected when any contributing entry was projected.
func TestPriceSummaries(t *testing.T) {
	s := openTestStore(t)

	id2019, err := s.AddContract(context.Background(), Contract{
		Name: "Point allocation", AnnualPoints: 120, UseYearMonth: 4,
		TermYears: 44, PurchasePrice: 2_940_000, ClosingCosts: 58_835,
	})
	if err != nil {
		t.Fatalf("AddContract: %v", err)
	}

	entries := []Entry{
		{UseYear: 2025, Date: date(t, "2025-05-01"), Desc: "allocation", Kind: KindAllocation, Allotted: 120},
		{UseYear: 2025, Date: date(t, "2025-06-01"), Desc: "usage A", Kind: KindUsage, Used: 100, ContractID: &id2019},
		{UseYear: 2025, Date: date(t, "2025-07-01"), Desc: "usage B", Kind: KindUsage, Used: 20, ContractID: &id2019},
		{UseYear: 2027, Date: date(t, "2027-06-01"), Desc: "projected", Kind: KindUsage, Used: 10, ContractID: &id2019},
	}
	for _, e := range entries {
		if _, err := s.AddEntry(context.Background(), e); err != nil {
			t.Fatalf("AddEntry(%s): %v", e.Desc, err)
		}
	}

	basis, err := s.CostBasis(context.Background())
	if err != nil {
		t.Fatalf("CostBasis: %v", err)
	}
	priced, err := s.ListEntries(context.Background())
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	basis.PriceEntries(priced)

	summaries, err := s.UseYearSummaries(context.Background())
	if err != nil {
		t.Fatalf("UseYearSummaries: %v", err)
	}
	PriceSummaries(summaries, priced)

	byYear := make(map[int]UseYearSummary, len(summaries))
	for _, sum := range summaries {
		byYear[sum.UseYear] = sum
	}

	rateFor2019 := basis.RateFor(&id2019)
	dues2025, _ := basis.DuesFor(2025)
	wantUY2025 := PointCost(100, rateFor2019, dues2025) + PointCost(20, rateFor2019, dues2025)
	got2025 := byYear[2025]
	if got2025.Cost != wantUY2025 || !got2025.CostKnown || got2025.CostProjected {
		t.Errorf("UY2025 summary = %+v, want Cost=%d CostKnown=true CostProjected=false", got2025, wantUY2025)
	}

	got2027 := byYear[2027]
	if !got2027.CostKnown || !got2027.CostProjected || got2027.Cost == 0 {
		t.Errorf("UY2027 summary = %+v, want CostKnown=true CostProjected=true Cost!=0", got2027)
	}
}
