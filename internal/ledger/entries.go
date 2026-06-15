package ledger

import "database/sql"

// AddEntry inserts e and returns its new id.
func (s *Store) AddEntry(e Entry) (int64, error) {
	res, err := s.db.Exec(
		`INSERT INTO entries (use_year, date, description, kind, allotted, used, tag, contract_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		e.UseYear, e.Date.Format(dateLayout), e.Desc, e.Kind, e.Allotted, e.Used, e.Tag, e.ContractID,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
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
		 SET use_year = ?, date = ?, description = ?, kind = ?, allotted = ?, used = ?, tag = ?, contract_id = ?
		 WHERE id = ?`,
		e.UseYear, e.Date.Format(dateLayout), e.Desc, e.Kind, e.Allotted, e.Used, e.Tag, e.ContractID, e.ID,
	)
	return err
}

// DeleteEntry removes the entry with the given id.
func (s *Store) DeleteEntry(id int64) error {
	_, err := s.db.Exec(`DELETE FROM entries WHERE id = ?`, id)
	return err
}
