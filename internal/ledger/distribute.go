package ledger

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/lineleader/lineleader/internal/ledger/dbgen"
)

// UseYearSummaries rolls every entry up by use year, ascending. Net is Allotted-Used;
// a negative Net (OverBorrowed) means that use year spent more than it took in.
func (s *Store) UseYearSummaries(ctx context.Context) ([]UseYearSummary, error) {
	rows, err := s.q.UseYearSummaries(ctx)
	if err != nil {
		return nil, err
	}

	var out []UseYearSummary
	for _, row := range rows {
		sum := UseYearSummary{
			UseYear:  int(row.UseYear),
			Allotted: int(row.Allotted),
			Used:     int(row.Used),
		}
		sum.Net = sum.Allotted - sum.Used
		sum.OverBorrowed = sum.Net < 0
		out = append(out, sum)
	}
	return out, nil
}

// DistributeNextYear posts next year's annual allocation for every contract that has
// already posted at least one allocation. "Next year" is the calendar year after the
// current one; running it more than once in the same year is a no-op (see
// distributeUpTo), so it is safe to trigger repeatedly. It creates rows only — banked
// points are not rolled forward. The newly created entries are returned.
func (s *Store) DistributeNextYear(ctx context.Context) ([]Entry, error) {
	return s.distributeUpTo(ctx, time.Now().Year()+1)
}

// distributeUpTo advances each contract by one use year, but never past maxYear. For a
// contract whose latest allocation is use year N, it inserts a use-year-(N+1)
// allocation dated on the contract's UseYearMonth using AnnualPoints — provided
// N+1 <= maxYear and that year is not already present. The maxYear cap is what makes
// repeat runs idempotent: once a contract reaches maxYear, the next target (maxYear+1)
// exceeds the cap and is skipped.
func (s *Store) distributeUpTo(ctx context.Context, maxYear int) ([]Entry, error) {
	contracts, err := s.ListContracts(ctx)
	if err != nil {
		return nil, err
	}

	var created []Entry
	for _, c := range contracts {
		latest, ok, err := s.latestAllocationYear(ctx, c.ID)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue // contract has never posted an allocation; nothing to advance from
		}
		target := latest + 1
		if target > maxYear {
			continue // already distributed up to the cap
		}
		if s.hasAllocationFor(ctx, c.ID, target) {
			continue
		}
		id := c.ID
		e := Entry{
			UseYear:    target,
			Date:       time.Date(target, c.UseYearMonth, 1, 0, 0, 0, 0, time.UTC),
			Desc:       c.Name,
			Kind:       KindAllocation,
			Allotted:   c.AnnualPoints,
			ContractID: &id,
		}
		newID, err := s.AddEntry(ctx, e)
		if err != nil {
			return nil, err
		}
		e.ID = newID
		created = append(created, e)
	}
	return created, nil
}

func (s *Store) latestAllocationYear(ctx context.Context, contractID int64) (int, bool, error) {
	year, err := s.q.LatestAllocationYear(ctx, dbgen.LatestAllocationYearParams{
		ContractID: sql.NullInt64{Int64: contractID, Valid: true},
		Kind:       KindAllocation,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return int(year), true, nil
}

func (s *Store) hasAllocationFor(ctx context.Context, contractID int64, useYear int) bool {
	n, _ := s.q.CountAllocationFor(ctx, dbgen.CountAllocationForParams{
		ContractID: sql.NullInt64{Int64: contractID, Valid: true},
		Kind:       KindAllocation,
		UseYear:    int32(useYear),
	})
	return n > 0
}
