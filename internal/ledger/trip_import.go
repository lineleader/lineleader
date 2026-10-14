package ledger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/lineleader/lineleader/internal/ledger/dbgen"
)

// Sentinel errors returned (wrapped with detail) by ImportBookedTrip.
var (
	ErrImportInvalid        = errors.New("invalid imported trip")
	ErrImportNoEntries      = errors.New("stay has no entry ids")
	ErrImportEntryNotFound  = errors.New("entry not found")
	ErrImportEntryNotUsage  = errors.New("entry is not a usage entry with used > 0")
	ErrImportEntryLinked    = errors.New("entry is already linked to a trip stay")
	ErrImportDuplicateEntry = errors.New("entry listed more than once")
	ErrImportPointsMismatch = errors.New("entries' used points do not sum to the stay's points")
)

// ImportedStay is a stay to import together with the ids of the EXISTING
// ledger usage entries that paid for it.
type ImportedStay struct {
	Stay     TripStay
	EntryIDs []int64
}

// ImportBookedTrip records a trip that was already booked (and already
// reconciled into the ledger by hand): it inserts the trip, its stays, and a
// trip_stay_entry link from each stay to its existing entries — all in one
// transaction. It deliberately bypasses the point allocator and creates no
// entries; booked-ness is derived from the links, so the stays read as booked
// and Unbook/DeleteTrip delete those entries. Any failure rolls back
// everything.
func (s *Store) ImportBookedTrip(ctx context.Context, t Trip, stays []ImportedStay) (int64, error) {
	if len(stays) == 0 {
		return 0, fmt.Errorf("%w: no stays", ErrImportInvalid)
	}
	if t.MinNights < 1 {
		t.MinNights = 1
	}
	if t.EndDate.Before(t.StartDate) {
		return 0, fmt.Errorf("%w: end date before start date", ErrImportInvalid)
	}
	for i, is := range stays {
		st := is.Stay
		if want := int(st.CheckOut.Sub(st.CheckIn).Hours() / 24); want < 1 || st.Nights != want {
			return 0, fmt.Errorf("%w: stay %d: nights = %d, but check-in to check-out is %d", ErrImportInvalid, i+1, st.Nights, want)
		}
		if st.Points < 0 {
			return 0, fmt.Errorf("%w: stay %d: negative points", ErrImportInvalid, i+1)
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("ImportBookedTrip: beginning transaction: %w", err)
	}
	defer tx.Rollback()
	txs := s.WithTx(tx)

	// Validate entries first, before writing anything.
	seen := map[int64]bool{}
	for i, is := range stays {
		if len(is.EntryIDs) == 0 {
			return 0, fmt.Errorf("stay %d: %w", i+1, ErrImportNoEntries)
		}
		sum := 0
		for _, eid := range is.EntryIDs {
			if seen[eid] {
				return 0, fmt.Errorf("stay %d: entry %d: %w", i+1, eid, ErrImportDuplicateEntry)
			}
			seen[eid] = true

			var kind string
			var used int
			err := tx.QueryRowContext(ctx, `SELECT kind, used FROM entry WHERE id = $1`, eid).Scan(&kind, &used)
			if errors.Is(err, sql.ErrNoRows) {
				return 0, fmt.Errorf("stay %d: entry %d: %w", i+1, eid, ErrImportEntryNotFound)
			}
			if err != nil {
				return 0, fmt.Errorf("ImportBookedTrip: reading entry %d: %w", eid, err)
			}
			if kind != KindUsage || used <= 0 {
				return 0, fmt.Errorf("stay %d: entry %d: %w", i+1, eid, ErrImportEntryNotUsage)
			}
			var linked bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM trip_stay_entry WHERE entry_id = $1)`, eid).Scan(&linked); err != nil {
				return 0, fmt.Errorf("ImportBookedTrip: checking links for entry %d: %w", eid, err)
			}
			if linked {
				return 0, fmt.Errorf("stay %d: entry %d: %w", i+1, eid, ErrImportEntryLinked)
			}
			sum += used
		}
		if sum != is.Stay.Points {
			return 0, fmt.Errorf("stay %d: entries sum to %d, stay has %d points: %w", i+1, sum, is.Stay.Points, ErrImportPointsMismatch)
		}
	}

	tripID, err := txs.AddTrip(ctx, t)
	if err != nil {
		return 0, fmt.Errorf("ImportBookedTrip: inserting trip: %w", err)
	}
	for _, is := range stays {
		st := is.Stay
		st.TripID = tripID
		stayID, err := txs.AddStay(ctx, st)
		if err != nil {
			return 0, fmt.Errorf("ImportBookedTrip: inserting stay: %w", err)
		}
		for _, eid := range is.EntryIDs {
			if err := txs.q.InsertTripStayEntry(ctx, dbgen.InsertTripStayEntryParams{TripStayID: stayID, EntryID: eid}); err != nil {
				return 0, fmt.Errorf("ImportBookedTrip: linking stay %d to entry %d: %w", stayID, eid, err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return tripID, nil
}
