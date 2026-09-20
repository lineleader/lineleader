package ledger

import (
	"context"
	"fmt"

	"github.com/lineleader/lineleader/internal/ledger/dbgen"
)

// BookTrip writes one usage entry per unbooked stay and links each stay to
// its new entry (a trip_stay_entry row), all in one transaction: either
// every stay is booked or none is. Re-booking is a no-op — only stays with
// no linked entries are considered (see TripStay.Booked) — so a
// double-submitted form is safe.
//
// Each entry's UseYear is UseYearForDate(stay.CheckIn, month), per stay and
// NOT per trip: a trip window may straddle a use-year boundary even though
// the displayed budget cannot.
//
// Tag is left empty. A trip can draw from current, banked and borrowed
// points at once with no per-stay attribution the app can honestly derive;
// the owner annotates Bank/Borrow by hand on /ledger if they care.
func (s *Store) BookTrip(ctx context.Context, tripID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("BookTrip: beginning transaction: %w", err)
	}
	defer tx.Rollback()

	txs := s.WithTx(tx)

	trip, err := txs.GetTrip(ctx, tripID)
	if err != nil {
		return fmt.Errorf("BookTrip: getting trip %d: %w", tripID, err)
	}

	contracts, err := txs.ListContracts(ctx)
	if err != nil {
		return fmt.Errorf("BookTrip: listing contracts: %w", err)
	}
	month := UseYearStartMonth(contracts)

	stays, err := txs.ListStays(ctx, tripID)
	if err != nil {
		return fmt.Errorf("BookTrip: listing stays for trip %d: %w", tripID, err)
	}

	for _, st := range stays {
		if st.Booked() {
			continue // already booked; re-booking must be a no-op
		}

		newID, err := txs.AddEntry(ctx, Entry{
			UseYear:  UseYearForDate(st.CheckIn, month),
			Date:     st.CheckIn,
			Desc:     trip.Name + " — " + st.Resort + " " + st.RoomType,
			Kind:     KindUsage,
			Used:     st.Points,
			Allotted: 0,
			Tag:      "",
		})
		if err != nil {
			return fmt.Errorf("BookTrip: adding entry for stay %d: %w", st.ID, err)
		}

		if err := txs.q.InsertTripStayEntry(ctx, dbgen.InsertTripStayEntryParams{
			TripStayID: st.ID,
			EntryID:    newID,
		}); err != nil {
			return fmt.Errorf("BookTrip: linking stay %d to entry %d: %w", st.ID, newID, err)
		}
	}

	return tx.Commit()
}

// UnbookTrip deletes every ledger entry this trip's stays created, reaching
// them only through trip_stay_entry and relying on that table's
// ON DELETE CASCADE (on entry_id) to remove the link rows as a side effect.
func (s *Store) UnbookTrip(ctx context.Context, tripID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("UnbookTrip: beginning transaction: %w", err)
	}
	defer tx.Rollback()

	txs := s.WithTx(tx)

	if err := txs.q.DeleteEntriesForTrip(ctx, tripID); err != nil {
		return fmt.Errorf("UnbookTrip: deleting entries for trip %d: %w", tripID, err)
	}

	return tx.Commit()
}

// DeleteTrip removes the trip, its stays (ON DELETE CASCADE) and every
// ledger entry those stays created.
//
// The entry deletion is NOT a side effect of the cascade: entry has no
// foreign key back to trip_stay, so cascading the stays away first would
// strand the usage rows in the ledger with no link and no way to reverse
// them. Both statements run in one transaction, entries FIRST.
func (s *Store) DeleteTrip(ctx context.Context, tripID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("DeleteTrip: beginning transaction: %w", err)
	}
	defer tx.Rollback()

	txs := s.WithTx(tx)

	if err := txs.q.DeleteEntriesForTrip(ctx, tripID); err != nil {
		return fmt.Errorf("DeleteTrip: deleting entries for trip %d: %w", tripID, err)
	}
	if err := txs.q.DeleteTrip(ctx, tripID); err != nil {
		return fmt.Errorf("DeleteTrip: deleting trip %d: %w", tripID, err)
	}

	return tx.Commit()
}

// DeleteStay removes one stay and, if it was booked, the ledger entry it
// created — same shape as DeleteTrip scoped to a single stay, and for the
// same reason.
func (s *Store) DeleteStay(ctx context.Context, stayID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("DeleteStay: beginning transaction: %w", err)
	}
	defer tx.Rollback()

	txs := s.WithTx(tx)

	if err := txs.q.DeleteEntriesForStay(ctx, stayID); err != nil {
		return fmt.Errorf("DeleteStay: deleting entry for stay %d: %w", stayID, err)
	}
	if err := txs.q.DeleteTripStay(ctx, stayID); err != nil {
		return fmt.Errorf("DeleteStay: deleting stay %d: %w", stayID, err)
	}

	return tx.Commit()
}
