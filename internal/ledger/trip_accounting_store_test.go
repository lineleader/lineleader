package ledger

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestLotSnapshotGroupsAllocationsByContractAndUseYear proves multiple
// allocation entries for the same (contract, use year) coalesce into a
// single PointLot rather than one row per entry — AllocateStayPoints
// expects one balance per (ContractID, UseYear), not per posted entry.
func TestLotSnapshotGroupsAllocationsByContractAndUseYear(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	contractID, err := s.AddContract(ctx, Contract{Name: "Point allocation", AnnualPoints: 220, UseYearMonth: time.April})
	if err != nil {
		t.Fatalf("AddContract: %v", err)
	}

	add := func(allotted int) {
		if _, err := s.AddEntry(ctx, Entry{UseYear: 2026, Date: date(t, "2026-04-01"), Kind: KindAllocation, Allotted: allotted, ContractID: &contractID}); err != nil {
			t.Fatalf("AddEntry: %v", err)
		}
	}
	add(120)
	add(100)

	_, lots, _, err := s.lotSnapshot(ctx, s.db, false)
	if err != nil {
		t.Fatalf("lotSnapshot: %v", err)
	}
	if len(lots) != 1 {
		t.Fatalf("lots = %+v, want exactly 1 coalesced lot", lots)
	}
	want := PointLot{ContractID: contractID, UseYear: 2026, Points: 220}
	if lots[0] != want {
		t.Errorf("lots[0] = %+v, want %+v", lots[0], want)
	}
}

// TestLotSnapshotIncludesBonusAndSingleUse is the explicit widening over the
// reference implementation on feat/durable-trips, which filtered
// kind = 'allocation' and silently excluded bonus and single_use points —
// real, already-posted points that would then be unspendable by the
// allocator. lotSnapshot must not filter on kind at all.
func TestLotSnapshotIncludesBonusAndSingleUse(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	contractID, err := s.AddContract(ctx, Contract{Name: "Point allocation", AnnualPoints: 120, UseYearMonth: time.April})
	if err != nil {
		t.Fatalf("AddContract: %v", err)
	}

	entries := []Entry{
		{UseYear: 2026, Date: date(t, "2026-04-01"), Kind: KindAllocation, Allotted: 120, ContractID: &contractID},
		{UseYear: 2026, Date: date(t, "2026-05-01"), Kind: KindBonus, Allotted: 25, ContractID: &contractID},
		{UseYear: 2026, Date: date(t, "2026-06-01"), Kind: KindSingleUse, Allotted: 10, ContractID: &contractID},
	}
	for _, e := range entries {
		if _, err := s.AddEntry(ctx, e); err != nil {
			t.Fatalf("AddEntry: %v", err)
		}
	}

	_, lots, _, err := s.lotSnapshot(ctx, s.db, false)
	if err != nil {
		t.Fatalf("lotSnapshot: %v", err)
	}
	if len(lots) != 1 {
		t.Fatalf("lots = %+v, want exactly 1 coalesced lot", lots)
	}
	want := PointLot{ContractID: contractID, UseYear: 2026, Points: 155} // 120 + 25 + 10
	if lots[0] != want {
		t.Errorf("lots[0] = %+v, want %+v (bonus and single_use must count)", lots[0], want)
	}
}

// TestLotSnapshotExcludesUnattributedAndUsage proves two exclusions at
// once: an allocation entry with no contract_id never becomes a lot (there
// is no contract to attribute it to), and a usage entry — even one WITH a
// contract_id — is never returned as a lot; it only ever shows up in the
// consumed list.
func TestLotSnapshotExcludesUnattributedAndUsage(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	contractID, err := s.AddContract(ctx, Contract{Name: "Point allocation", AnnualPoints: 120, UseYearMonth: time.April})
	if err != nil {
		t.Fatalf("AddContract: %v", err)
	}

	// Attributed allocation: should appear in lots.
	if _, err := s.AddEntry(ctx, Entry{UseYear: 2026, Date: date(t, "2026-04-01"), Kind: KindAllocation, Allotted: 120, ContractID: &contractID}); err != nil {
		t.Fatalf("AddEntry: %v", err)
	}
	// Unattributed allocation: no contract_id, must be excluded from lots.
	if _, err := s.AddEntry(ctx, Entry{UseYear: 2026, Date: date(t, "2026-04-02"), Kind: KindAllocation, Allotted: 50}); err != nil {
		t.Fatalf("AddEntry: %v", err)
	}
	// Attributed usage: must land in consumed, never in lots.
	if _, err := s.AddEntry(ctx, Entry{UseYear: 2026, Date: date(t, "2026-06-01"), Kind: KindUsage, Used: 30, ContractID: &contractID}); err != nil {
		t.Fatalf("AddEntry: %v", err)
	}

	_, lots, consumed, err := s.lotSnapshot(ctx, s.db, false)
	if err != nil {
		t.Fatalf("lotSnapshot: %v", err)
	}
	if len(lots) != 1 {
		t.Fatalf("lots = %+v, want exactly 1 (the attributed allocation only)", lots)
	}
	if want := (PointLot{ContractID: contractID, UseYear: 2026, Points: 120}); lots[0] != want {
		t.Errorf("lots[0] = %+v, want %+v", lots[0], want)
	}
	if len(consumed) != 1 {
		t.Fatalf("consumed = %+v, want exactly 1", consumed)
	}
	if want := (PointDraw{ContractID: contractID, UseYear: 2026, Points: 30}); consumed[0] != want {
		t.Errorf("consumed[0] = %+v, want %+v", consumed[0], want)
	}
}

// TestUnattributedUsage proves the query sums exactly the contract-less
// usage entries — hand-entered /ledger usage and the rows migration 00005
// backfilled — and ignores usage that IS attributed to a contract.
func TestUnattributedUsage(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	contractID, err := s.AddContract(ctx, Contract{Name: "Point allocation", AnnualPoints: 120, UseYearMonth: time.April})
	if err != nil {
		t.Fatalf("AddContract: %v", err)
	}

	if _, err := s.AddEntry(ctx, Entry{UseYear: 2026, Date: date(t, "2026-05-01"), Kind: KindUsage, Used: 50}); err != nil {
		t.Fatalf("AddEntry: %v", err)
	}
	if _, err := s.AddEntry(ctx, Entry{UseYear: 2026, Date: date(t, "2026-06-01"), Kind: KindUsage, Used: 30, ContractID: &contractID}); err != nil {
		t.Fatalf("AddEntry: %v", err)
	}

	got, err := s.unattributedUsage(ctx, s.db)
	if err != nil {
		t.Fatalf("unattributedUsage: %v", err)
	}
	if got != 50 {
		t.Errorf("unattributedUsage() = %d, want 50 (contract-attributed usage must be excluded)", got)
	}
}

// TestPreviewStayFundingSplitsAcrossContracts reproduces the epic's worked
// example end to end through real contracts and entries: two contracts
// with 95 and 100 points left, respectively, and a 150-point stay that must
// draw 95 from the first (exhausting it) then 55 from the second.
func TestPreviewStayFundingSplitsAcrossContracts(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	c1, err := s.AddContract(ctx, Contract{Name: "Point allocation", AnnualPoints: 95, UseYearMonth: time.April})
	if err != nil {
		t.Fatalf("AddContract: %v", err)
	}
	c2, err := s.AddContract(ctx, Contract{Name: "Point allocation #2", AnnualPoints: 100, UseYearMonth: time.April})
	if err != nil {
		t.Fatalf("AddContract: %v", err)
	}
	if _, err := s.AddEntry(ctx, Entry{UseYear: 2026, Date: date(t, "2026-04-01"), Kind: KindAllocation, Allotted: 95, ContractID: &c1}); err != nil {
		t.Fatalf("AddEntry: %v", err)
	}
	if _, err := s.AddEntry(ctx, Entry{UseYear: 2026, Date: date(t, "2026-04-01"), Kind: KindAllocation, Allotted: 100, ContractID: &c2}); err != nil {
		t.Fatalf("AddEntry: %v", err)
	}

	got, err := s.PreviewStayFunding(ctx, date(t, "2026-06-01"), 150)
	if err != nil {
		t.Fatalf("PreviewStayFunding: %v", err)
	}
	want := []PointDraw{
		{ContractID: c1, UseYear: 2026, Disposition: DispositionCurrent, Points: 95},
		{ContractID: c2, UseYear: 2026, Disposition: DispositionCurrent, Points: 55},
	}
	if len(got) != len(want) {
		t.Fatalf("draws = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("draw[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestPreviewStayFundingInsufficientWithLegacyUsage proves the legacy-usage
// term is actually wired into the fast-fail check: the lots alone
// (195 points) are enough to cover a 150-point stay, but 50 points of
// contract-less legacy usage push the available total to 145, which must
// fail with ErrInsufficientPoints and a nil slice, not a partial
// allocation.
func TestPreviewStayFundingInsufficientWithLegacyUsage(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	c1, err := s.AddContract(ctx, Contract{Name: "Point allocation", AnnualPoints: 95, UseYearMonth: time.April})
	if err != nil {
		t.Fatalf("AddContract: %v", err)
	}
	c2, err := s.AddContract(ctx, Contract{Name: "Point allocation #2", AnnualPoints: 100, UseYearMonth: time.April})
	if err != nil {
		t.Fatalf("AddContract: %v", err)
	}
	if _, err := s.AddEntry(ctx, Entry{UseYear: 2026, Date: date(t, "2026-04-01"), Kind: KindAllocation, Allotted: 95, ContractID: &c1}); err != nil {
		t.Fatalf("AddEntry: %v", err)
	}
	if _, err := s.AddEntry(ctx, Entry{UseYear: 2026, Date: date(t, "2026-04-01"), Kind: KindAllocation, Allotted: 100, ContractID: &c2}); err != nil {
		t.Fatalf("AddEntry: %v", err)
	}
	// Legacy usage with no contract attribution: hand-entered /ledger usage
	// or a 00005 backfill row.
	if _, err := s.AddEntry(ctx, Entry{UseYear: 2026, Date: date(t, "2026-05-01"), Kind: KindUsage, Used: 50}); err != nil {
		t.Fatalf("AddEntry: %v", err)
	}

	got, err := s.PreviewStayFunding(ctx, date(t, "2026-06-01"), 150)
	if !errors.Is(err, ErrInsufficientPoints) {
		t.Fatalf("err = %v, want ErrInsufficientPoints", err)
	}
	if got != nil {
		t.Errorf("draws = %+v, want nil", got)
	}
}
