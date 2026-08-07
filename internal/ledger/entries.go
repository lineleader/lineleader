package ledger

import "database/sql"

// AddEntry inserts e and returns its new id.
func (s *Store) AddEntry(e Entry) (int64, error) {
	var id int64
	err := s.db.QueryRow(
		`INSERT INTO entries (use_year, date, description, kind, allotted, used, tag, contract_id)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`,
		e.UseYear, e.Date.Format(DateLayout), e.Desc, e.Kind, e.Allotted, e.Used, e.Tag, e.ContractID,
	).Scan(&id)
	if err != nil {
		return 0, err
	}
	return id, nil
}

// ListEntries returns every entry ordered by (date, id) with RunningBalance computed
// as the cumulative (Allotted - Used) down the list. The running balance is derived
// here, never stored, so negative balances (borrowing) fall out naturally.
func (s *Store) ListEntries() ([]Entry, error) {
	rows, err := s.db.Query(
		`SELECT id, use_year, date, description, kind, allotted, used, tag, contract_id
		 FROM entries ORDER BY date, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Entry
	balance := 0
	for rows.Next() {
		var (
			e          Entry
			dateStr    string
			contractID sql.NullInt64
		)
		if err := rows.Scan(&e.ID, &e.UseYear, &dateStr, &e.Desc, &e.Kind, &e.Allotted, &e.Used, &e.Tag, &contractID); err != nil {
			return nil, err
		}
		e.Date, err = parseDate(dateStr)
		if err != nil {
			return nil, err
		}
		if contractID.Valid {
			id := contractID.Int64
			e.ContractID = &id
		}
		balance += e.Allotted - e.Used
		e.RunningBalance = balance
		out = append(out, e)
	}
	return out, rows.Err()
}

// UpdateEntry overwrites the entry identified by e.ID.
func (s *Store) UpdateEntry(e Entry) error {
	_, err := s.db.Exec(
		`UPDATE entries
		 SET use_year = $1, date = $2, description = $3, kind = $4, allotted = $5, used = $6, tag = $7, contract_id = $8
		 WHERE id = $9`,
		e.UseYear, e.Date.Format(DateLayout), e.Desc, e.Kind, e.Allotted, e.Used, e.Tag, e.ContractID, e.ID,
	)
	return err
}

// DeleteEntry removes the entry with the given id.
func (s *Store) DeleteEntry(id int64) error {
	_, err := s.db.Exec(`DELETE FROM entries WHERE id = $1`, id)
	return err
}
