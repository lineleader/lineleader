package ledger

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// queryer and rowQueryer are satisfied by both *sql.DB and *sql.Tx, so the
// reads below run identically whether called outside a transaction (this
// issue's read-only preview) or inside one (nyj.4's locked booking
// transaction).
type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}
type rowQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// lotSnapshot reads everything AllocateStayPoints needs to decide a stay's
// funding: every contract (for its UseYearMonth), every lot of points a
// contract has been given, and every point already drawn against those
// lots. It never writes.
//
// lock selects whether the first two reads (contracts and lots — the rows
// nyj.4's booking transaction actually needs to hold stable while it
// decides and then posts a draw) end in FOR UPDATE. Nothing in this issue
// calls lotSnapshot with lock=true; PreviewStayFunding always passes false,
// since a dry-run preview has nothing to protect. It's built now so nyj.4
// can call lotSnapshot(ctx, tx, true) inside a serializable transaction
// without another change to this file.
func (s *Store) lotSnapshot(ctx context.Context, q queryer, lock bool) ([]Contract, []PointLot, []PointDraw, error) {
	lockClause := ""
	if lock {
		lockClause = "\n\t\t\tFOR UPDATE"
	}

	contracts, err := lotSnapshotContracts(ctx, q, lockClause)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("lotSnapshot: %w", err)
	}
	lots, err := lotSnapshotLots(ctx, q, lockClause)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("lotSnapshot: %w", err)
	}
	consumed, err := lotSnapshotConsumed(ctx, q)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("lotSnapshot: %w", err)
	}
	return contracts, lots, consumed, nil
}

// lotSnapshotContracts reads every contract's id and UseYearMonth — the
// only two fields AllocateStayPoints consults (see its own doc comment).
// No aggregation here, so the lock clause can simply be appended.
func lotSnapshotContracts(ctx context.Context, q queryer, lockClause string) ([]Contract, error) {
	query := `SELECT id, use_year_month FROM contract ORDER BY id` + lockClause
	rows, err := q.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("querying contracts: %w", err)
	}
	defer rows.Close()

	var contracts []Contract
	for rows.Next() {
		var c Contract
		var month int
		if err := rows.Scan(&c.ID, &month); err != nil {
			return nil, fmt.Errorf("scanning contract: %w", err)
		}
		c.UseYearMonth = time.Month(month)
		contracts = append(contracts, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating contracts: %w", err)
	}
	return contracts, nil
}

// lotSnapshotLots reads every (contract, use year) that has been given
// points and how many, summed and grouped.
//
// It does NOT filter on kind. A reference implementation of a similar
// snapshot (feat/durable-trips) filtered kind = 'allocation', which
// silently excluded bonus (promotional points) and single_use (purchased
// points) entries — both carry a contract_id and a positive Allotted, and
// both are real, already-posted points a member can spend. Filtering them
// out here would make them permanently unspendable by the allocator, so
// every kind that adds points to a contract's pool (allocation, bonus,
// single_use, and even a corrective adjustment with a positive Allotted)
// is counted.
//
// Postgres rejects FOR UPDATE together with GROUP BY (a locking clause
// must identify individual table rows, and a SUM(...) row doesn't
// correspond to one), so when lock is requested it's applied inside the
// derived table below — locking every contributing entry row — rather
// than on this query's own aggregated output.
func lotSnapshotLots(ctx context.Context, q queryer, lockClause string) ([]PointLot, error) {
	query := `
		SELECT contract_id, use_year, SUM(allotted)::bigint AS points
		FROM (
			SELECT contract_id, use_year, allotted
			FROM entry
			WHERE allotted > 0 AND contract_id IS NOT NULL` + lockClause + `
		) allocating_entries
		GROUP BY contract_id, use_year
		ORDER BY contract_id, use_year`
	rows, err := q.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("querying lots: %w", err)
	}
	defer rows.Close()

	var lots []PointLot
	for rows.Next() {
		var lot PointLot
		if err := rows.Scan(&lot.ContractID, &lot.UseYear, &lot.Points); err != nil {
			return nil, fmt.Errorf("scanning lot: %w", err)
		}
		lots = append(lots, lot)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating lots: %w", err)
	}
	return lots, nil
}

// lotSnapshotConsumed reads every (contract, use year) that has had points
// drawn against it and how many, summed and grouped, as the []PointDraw
// AllocateStayPoints expects for its consumed argument.
//
// Disposition is left at its zero value. AllocateStayPoints only ever uses
// consumed draws to reduce a lot's remaining balance (Points minus every
// matching consumed draw) — it never inspects a consumed draw's
// Disposition, only the Disposition it assigns to its OWN output — so there
// is nothing meaningful to populate here.
//
// This is never locked (see lotSnapshot's doc comment): nyj.4 only needs
// contracts and lots held stable while it decides a new draw; the
// already-consumed total it's reading against is history, not something
// concurrent bookings threaten.
func lotSnapshotConsumed(ctx context.Context, q queryer) ([]PointDraw, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT contract_id, use_year, SUM(used)::bigint AS points
		FROM entry
		WHERE used > 0 AND contract_id IS NOT NULL
		GROUP BY contract_id, use_year
		ORDER BY contract_id, use_year`)
	if err != nil {
		return nil, fmt.Errorf("querying consumed: %w", err)
	}
	defer rows.Close()

	var consumed []PointDraw
	for rows.Next() {
		var d PointDraw
		if err := rows.Scan(&d.ContractID, &d.UseYear, &d.Points); err != nil {
			return nil, fmt.Errorf("scanning consumed draw: %w", err)
		}
		consumed = append(consumed, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating consumed: %w", err)
	}
	return consumed, nil
}

// unattributedUsage sums every usage entry with no contract_id: hand-entered
// /ledger usage from before this allocator existed, plus the rows migration
// 00005 backfilled when trip_stay_entry replaced trip_stay.entry_id. None of
// it can be attributed to a specific contract's lot after the fact — there
// is no record of which contract actually funded it — so rather than invent
// an attribution nobody computed, it's subtracted from the aggregate
// available total instead: old usage still counts against the balance, it
// just isn't drawn from any one lot.
func (s *Store) unattributedUsage(ctx context.Context, q rowQueryer) (int, error) {
	var used int
	err := q.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(used), 0) FROM entry WHERE used > 0 AND contract_id IS NULL`).Scan(&used)
	if err != nil {
		return 0, fmt.Errorf("unattributedUsage: %w", err)
	}
	return used, nil
}

// availablePoints sums every lot's points, less every consumed draw against
// those lots, less legacyUsed (unattributedUsage's legacy, contract-less
// usage). This is the aggregate total PreviewStayFunding and BookTrip both
// fast-fail against before delegating to AllocateStayPoints, which has no
// visibility into unattributed legacy usage on its own — nothing in its
// consumed argument carries it, since it has no ContractID to key against.
func availablePoints(lots []PointLot, consumed []PointDraw, legacyUsed int) int {
	available := -legacyUsed
	for _, lot := range lots {
		available += lot.Points
	}
	for _, draw := range consumed {
		available -= draw.Points
	}
	return available
}

// PreviewStayFunding is a dry-run of AllocateStayPoints against the current
// ledger: unlocked, outside any transaction, and it writes nothing. It
// exists for the booking UI (nyj.5) to show a member which lots a stay
// would draw from before they commit to booking it. The draws it returns
// are never persisted by this function — nyj.4's actual booking path takes
// its own lotSnapshot inside a locked, serializable transaction rather than
// trusting a preview that may be stale by the time the member confirms.
//
// Before delegating to AllocateStayPoints, it fast-fails with
// ErrInsufficientPoints if points exceeds availablePoints' aggregate total.
// AllocateStayPoints would eventually reach the same conclusion on its own
// — its candidate walk simply runs out of lots — but only after checking
// use-year eligibility per lot; this check is a plain aggregate total, so it
// also catches the case AllocateStayPoints cannot see at all: enough points
// exist across all lots, but old, unattributed usage has already spent
// them.
func (s *Store) PreviewStayFunding(ctx context.Context, checkIn time.Time, points int) ([]PointDraw, error) {
	contracts, lots, consumed, err := s.lotSnapshot(ctx, s.db, false)
	if err != nil {
		return nil, fmt.Errorf("PreviewStayFunding: %w", err)
	}
	legacyUsed, err := s.unattributedUsage(ctx, s.db)
	if err != nil {
		return nil, fmt.Errorf("PreviewStayFunding: %w", err)
	}

	if points > availablePoints(lots, consumed, legacyUsed) {
		return nil, ErrInsufficientPoints
	}

	return AllocateStayPoints(contracts, lots, consumed, checkIn, points)
}
