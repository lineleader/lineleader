package ledger

import "time"

// UseYearSummaries rolls every entry up by use year, ascending. Net is Allotted-Used;
// a negative Net (OverBorrowed) means that use year spent more than it took in.
func (s *Store) UseYearSummaries() ([]UseYearSummary, error) {
	rows, err := s.db.Query(
		`SELECT use_year, COALESCE(SUM(allotted), 0), COALESCE(SUM(used), 0)
		 FROM entries GROUP BY use_year ORDER BY use_year`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []UseYearSummary
	for rows.Next() {
		var sum UseYearSummary
		if err := rows.Scan(&sum.UseYear, &sum.Allotted, &sum.Used); err != nil {
			return nil, err
		}
		sum.Net = sum.Allotted - sum.Used
		sum.OverBorrowed = sum.Net < 0
		out = append(out, sum)
	}
	return out, rows.Err()
}

// DistributeNextYear posts next year's annual allocation for every contract that has
// already posted at least one allocation. "Next year" is the calendar year after the
// current one; running it more than once in the same year is a no-op (see
// distributeUpTo), so it is safe to trigger repeatedly. It creates rows only — banked
// points are not rolled forward. The newly created entries are returned.
func (s *Store) DistributeNextYear() ([]Entry, error) {
	return s.distributeUpTo(time.Now().Year() + 1)
}

// distributeUpTo advances each contract by one use year, but never past maxYear. For a
// contract whose latest allocation is use year N, it inserts a use-year-(N+1)
// allocation dated on the contract's UseYearMonth using AnnualPoints — provided
// N+1 <= maxYear and that year is not already present. The maxYear cap is what makes
// repeat runs idempotent: once a contract reaches maxYear, the next target (maxYear+1)
// exceeds the cap and is skipped.
func (s *Store) distributeUpTo(maxYear int) ([]Entry, error) {
	contracts, err := s.ListContracts()
	if err != nil {
		return nil, err
	}

	var created []Entry
	for _, c := range contracts {
		latest, ok, err := s.latestAllocationYear(c.ID)
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
		if s.hasAllocationFor(c.ID, target) {
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
		newID, err := s.AddEntry(e)
		if err != nil {
			return nil, err
		}
		e.ID = newID
		created = append(created, e)
	}
	return created, nil
}

func (s *Store) latestAllocationYear(contractID int64) (int, bool, error) {
	rows, err := s.db.Query(
		`SELECT use_year FROM entries
		 WHERE contract_id = $1 AND kind = $2
		 ORDER BY use_year DESC LIMIT 1`, contractID, KindAllocation)
	if err != nil {
		return 0, false, err
	}
	defer rows.Close()
	if !rows.Next() {
		return 0, false, rows.Err()
	}
	var year int
	if err := rows.Scan(&year); err != nil {
		return 0, false, err
	}
	return year, true, nil
}

func (s *Store) hasAllocationFor(contractID int64, useYear int) bool {
	var n int
	_ = s.db.QueryRow(
		`SELECT COUNT(1) FROM entries
		 WHERE contract_id = $1 AND kind = $2 AND use_year = $3`,
		contractID, KindAllocation, useYear).Scan(&n)
	return n > 0
}
