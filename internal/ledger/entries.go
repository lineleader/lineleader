package ledger

import (
	"context"
	"database/sql"

	"github.com/lineleader/lineleader/internal/ledger/dbgen"
)

// contractIDParam converts a possibly-nil ContractID into the sql.NullInt64
// dbgen's generated params expect for the nullable entries.contract_id
// column.
func contractIDParam(id *int64) sql.NullInt64 {
	if id == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *id, Valid: true}
}

// entryFromRow maps a dbgen.Entry (sqlc's generated model) onto the domain
// Entry type. entries.date is stored as TEXT (see parseDate for the two
// formats tolerated); RunningBalance, Cost, CostKnown and CostProjected are
// derived elsewhere and are left at their zero value here.
func entryFromRow(row dbgen.Entry) (Entry, error) {
	date, err := parseDate(row.Date)
	if err != nil {
		return Entry{}, err
	}
	e := Entry{
		ID:       row.ID,
		UseYear:  int(row.UseYear),
		Date:     date,
		Desc:     row.Description,
		Kind:     row.Kind,
		Allotted: int(row.Allotted),
		Used:     int(row.Used),
		Tag:      row.Tag,
	}
	if row.ContractID.Valid {
		id := row.ContractID.Int64
		e.ContractID = &id
	}
	return e, nil
}

// AddEntry inserts e and returns its new id.
func (s *Store) AddEntry(ctx context.Context, e Entry) (int64, error) {
	return s.q.InsertEntry(ctx, dbgen.InsertEntryParams{
		UseYear:     int32(e.UseYear),
		Date:        e.Date.Format(DateLayout),
		Description: e.Desc,
		Kind:        e.Kind,
		Allotted:    int32(e.Allotted),
		Used:        int32(e.Used),
		Tag:         e.Tag,
		ContractID:  contractIDParam(e.ContractID),
	})
}

// ListEntries returns every entry ordered by (date, id) with RunningBalance computed
// as the cumulative (Allotted - Used) down the list. The running balance is derived
// here, never stored, so negative balances (borrowing) fall out naturally.
func (s *Store) ListEntries(ctx context.Context) ([]Entry, error) {
	rows, err := s.q.ListEntries(ctx)
	if err != nil {
		return nil, err
	}

	var out []Entry
	balance := 0
	for _, row := range rows {
		e, err := entryFromRow(row)
		if err != nil {
			return nil, err
		}
		balance += e.Allotted - e.Used
		e.RunningBalance = balance
		out = append(out, e)
	}
	return out, nil
}

// UpdateEntry overwrites the entry identified by e.ID.
func (s *Store) UpdateEntry(ctx context.Context, e Entry) error {
	return s.q.UpdateEntry(ctx, dbgen.UpdateEntryParams{
		UseYear:     int32(e.UseYear),
		Date:        e.Date.Format(DateLayout),
		Description: e.Desc,
		Kind:        e.Kind,
		Allotted:    int32(e.Allotted),
		Used:        int32(e.Used),
		Tag:         e.Tag,
		ContractID:  contractIDParam(e.ContractID),
		ID:          e.ID,
	})
}

// DeleteEntry removes the entry with the given id.
func (s *Store) DeleteEntry(ctx context.Context, id int64) error {
	return s.q.DeleteEntry(ctx, id)
}
