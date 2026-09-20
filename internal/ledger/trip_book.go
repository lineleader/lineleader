package ledger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/lineleader/lineleader/internal/ledger/dbgen"
)

// DispositionTag maps a PointDraw's Disposition to the Entry.Tag convention
// documented in types.go ("Bank" | "Borrow" | ""): banked points are
// tagged "Bank", borrowed points "Borrow", and current-year points get no
// annotation at all — the common case needs none. Exported so nyj.5's
// funding preview (trip_accounting.go) can show the web layer the same
// wording BookTrip actually writes to the ledger, rather than inventing
// its own.
func DispositionTag(disposition string) string {
	switch disposition {
	case DispositionBanked:
		return "Bank"
	case DispositionBorrowed:
		return "Borrow"
	default:
		return ""
	}
}

// bookTripAttempts bounds BookTrip's retry loop around serialization
// failures (SQLSTATE 40001). The standard contract for SERIALIZABLE
// isolation is that a client retries a transaction that loses a
// serialization conflict; 3 attempts is generous for two bookings racing
// over the same lots — the loser's retry begins with a fresh, unlocked
// snapshot and either finds the points still there or, if not, fails
// through AllocateStayPoints' own ErrInsufficientPoints rather than needing
// another retry.
const bookTripAttempts = 3

// BookTrip funds every unbooked stay on tripID through the point allocator
// (AllocateStayPoints), recording which contract and use year paid for each
// draw, and links each stay to every entry its funding produced (one or more
// trip_stay_entry rows per stay — a stay short one lot's balance spills into
// the next candidate lot). Re-booking is a no-op — only stays with no linked
// entries are considered (see TripStay.Booked) — so a double-submitted form
// is safe.
//
// Either every unbooked stay is booked or none is. Stays are allocated in
// ListStays' stable (check_in, id) order, and each stay's draws are appended
// to the in-memory `consumed` list before the next stay is allocated, so a
// multi-stay booking can never spend the same points twice within its own
// transaction — lotSnapshot's own `consumed` read only reflects draws
// already committed before this transaction began. If any stay's allocation
// comes back ErrInsufficientPoints, the whole attempt is rolled back and
// BookTrip returns that error — nothing is written; partially booking a
// trip is not acceptable.
//
// The transaction runs at SERIALIZABLE isolation and takes a locked lot
// snapshot (lotSnapshot(ctx, tx, true)) so two concurrent bookings racing
// for the same last points can't both succeed; see lotSnapshot's doc
// comment for why the lock is a derived-table FOR UPDATE rather than one on
// the aggregated query itself. A transaction that loses that race aborts
// with SQLSTATE 40001 (serialization_failure), which the caller
// (bookTripAttempts retries) retries with a fresh snapshot — see BookTrip's
// wrapper for why that retry never needs to translate a raw driver error
// into ErrInsufficientPoints itself.
func (s *Store) bookTripOnce(ctx context.Context, tripID int64) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("BookTrip: beginning transaction: %w", err)
	}
	defer tx.Rollback()

	txs := s.WithTx(tx)

	trip, err := txs.GetTrip(ctx, tripID)
	if err != nil {
		return fmt.Errorf("BookTrip: getting trip %d: %w", tripID, err)
	}

	stays, err := txs.ListStays(ctx, tripID)
	if err != nil {
		return fmt.Errorf("BookTrip: listing stays for trip %d: %w", tripID, err)
	}

	contracts, lots, consumed, err := s.lotSnapshot(ctx, tx, true)
	if err != nil {
		return fmt.Errorf("BookTrip: %w", err)
	}
	legacyUsed, err := s.unattributedUsage(ctx, tx)
	if err != nil {
		return fmt.Errorf("BookTrip: %w", err)
	}

	for _, st := range stays {
		if st.Booked() {
			continue // already booked; re-booking must be a no-op
		}

		if st.Points > availablePoints(lots, consumed, legacyUsed) {
			return ErrInsufficientPoints
		}

		draws, err := AllocateStayPoints(contracts, lots, consumed, st.CheckIn, st.Points)
		if err != nil {
			return fmt.Errorf("BookTrip: allocating stay %d: %w", st.ID, err)
		}

		for _, draw := range draws {
			newID, err := txs.AddEntry(ctx, Entry{
				UseYear:    draw.UseYear,
				Date:       st.CheckIn,
				Desc:       trip.Name + " — " + st.Resort + " " + st.RoomType,
				Kind:       KindUsage,
				Allotted:   0,
				Used:       draw.Points,
				ContractID: &draw.ContractID,
				Tag:        DispositionTag(draw.Disposition),
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

		// Must happen before the next stay is allocated: consumed is what
		// keeps a later stay in this same booking from drawing against
		// balance this stay already spent.
		consumed = append(consumed, draws...)
	}

	return tx.Commit()
}

// BookTrip is bookTripOnce wrapped in a bounded retry (bookTripAttempts) that
// retries only a transaction that lost a serialization conflict (SQLSTATE
// 40001) — any other error, including ErrInsufficientPoints, is returned
// immediately without a retry.
func (s *Store) BookTrip(ctx context.Context, tripID int64) error {
	var err error
	for attempt := 0; attempt < bookTripAttempts; attempt++ {
		err = s.bookTripOnce(ctx, tripID)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "40001" {
			continue
		}
		return err
	}
	return err
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
