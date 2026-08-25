package main

import (
	"database/sql"
	"fmt"

	"github.com/lineleader/lineleader/internal/ledger"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

// Summary reports what Migrate copied.
type Summary struct {
	Contracts int
	Entries   int
}

// sqliteContract and sqliteEntry hold raw column values read from the
// legacy SQLite ledger — nothing is re-derived, only copied.
type sqliteContract struct {
	ID           int64
	Name         string
	Number       string
	HomeResort   string
	AnnualPoints int64
	UseYearMonth int64
}

type sqliteEntry struct {
	ID          int64
	UseYear     int64
	Date        string
	Description string
	Kind        string
	Allotted    int64
	Used        int64
	Tag         string
	ContractID  sql.NullInt64
}

// Migrate copies every contract and entry from the SQLite ledger at
// sqlitePath into the Postgres database identified by pgDSN, preserving
// row ids and the entries.contract_id foreign key exactly.
//
// It refuses to run if the Postgres side already has any rows in contracts
// or entries: this is a one-shot tool, not a sync. The whole copy runs in a
// single transaction, and each table's identity sequence is reset to
// max(id)+1 afterward so subsequent inserts don't collide with migrated ids.
func Migrate(sqlitePath, pgDSN string) (Summary, error) {
	sdb, err := sql.Open("sqlite", sqlitePath)
	if err != nil {
		return Summary{}, fmt.Errorf("opening sqlite %s: %w", sqlitePath, err)
	}
	defer sdb.Close()

	contracts, err := readSQLiteContracts(sdb)
	if err != nil {
		return Summary{}, fmt.Errorf("reading contracts: %w", err)
	}
	entries, err := readSQLiteEntries(sdb)
	if err != nil {
		return Summary{}, fmt.Errorf("reading entries: %w", err)
	}

	// ledger.Open applies the (idempotent) Postgres schema on connect; we
	// then use a raw *sql.DB of our own for the id-preserving inserts,
	// since Store's connection isn't exposed.
	store, err := ledger.Open(pgDSN)
	if err != nil {
		return Summary{}, fmt.Errorf("opening postgres: %w", err)
	}
	if err := store.Close(); err != nil {
		return Summary{}, fmt.Errorf("closing schema-setup connection: %w", err)
	}

	pdb, err := sql.Open("pgx", pgDSN)
	if err != nil {
		return Summary{}, fmt.Errorf("opening postgres: %w", err)
	}
	defer pdb.Close()

	if err := ensureTargetEmpty(pdb); err != nil {
		return Summary{}, err
	}

	if err := copyAll(pdb, contracts, entries); err != nil {
		return Summary{}, err
	}

	return Summary{Contracts: len(contracts), Entries: len(entries)}, nil
}

// ensureTargetEmpty is the one-shot-tool safety check: refuse to touch a
// Postgres database that already has ledger rows in it.
func ensureTargetEmpty(db *sql.DB) error {
	var contracts, entries int
	if err := db.QueryRow(`SELECT count(*) FROM contract`).Scan(&contracts); err != nil {
		return fmt.Errorf("checking target contracts: %w", err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM entry`).Scan(&entries); err != nil {
		return fmt.Errorf("checking target entries: %w", err)
	}
	if contracts > 0 || entries > 0 {
		return fmt.Errorf(
			"refusing to migrate: target Postgres already has %d contract(s) and %d entries(s) — "+
				"this is a one-shot tool, not a sync; point --dsn at an empty database",
			contracts, entries,
		)
	}
	return nil
}

// copyAll inserts contracts then entries — in that order, so entries'
// contract_id foreign keys are satisfiable — inside a single transaction,
// then resets both identity sequences before committing.
func copyAll(db *sql.DB, contracts []sqliteContract, entries []sqliteEntry) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("starting transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op once Commit succeeds

	for _, c := range contracts {
		if _, err := tx.Exec(
			`INSERT INTO contract (id, name, number, home_resort, annual_points, use_year_month)
			 OVERRIDING SYSTEM VALUE VALUES ($1, $2, $3, $4, $5, $6)`,
			c.ID, c.Name, c.Number, c.HomeResort, c.AnnualPoints, c.UseYearMonth,
		); err != nil {
			return fmt.Errorf("inserting contract %d: %w", c.ID, err)
		}
	}

	for _, e := range entries {
		if _, err := tx.Exec(
			`INSERT INTO entry (id, use_year, date, description, kind, allotted, used, tag, contract_id)
			 OVERRIDING SYSTEM VALUE VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			e.ID, e.UseYear, e.Date, e.Description, e.Kind, e.Allotted, e.Used, e.Tag, e.ContractID,
		); err != nil {
			return fmt.Errorf("inserting entry %d: %w", e.ID, err)
		}
	}

	if err := resetIdentitySequence(tx, "contract"); err != nil {
		return err
	}
	if err := resetIdentitySequence(tx, "entry"); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing: %w", err)
	}
	return nil
}

// resetIdentitySequence advances table's GENERATED ALWAYS AS IDENTITY
// sequence past the highest id just inserted with OVERRIDING SYSTEM VALUE.
// Without this, the next ordinary insert reuses a migrated id and collides.
//
// The three-argument form of setval sets is_called explicitly instead of
// relying on its default-true behavior: when the table has rows, is_called
// = true makes the next nextval() return max(id)+1 (what we want); when the
// table is empty, is_called = false makes the next nextval() return 1
// rather than skipping straight to 2.
func resetIdentitySequence(tx *sql.Tx, table string) error {
	_, err := tx.Exec(fmt.Sprintf(`
		SELECT setval(
			pg_get_serial_sequence('%[1]s', 'id'),
			COALESCE((SELECT max(id) FROM %[1]s), 1),
			(SELECT max(id) FROM %[1]s) IS NOT NULL
		)`, table))
	if err != nil {
		return fmt.Errorf("resetting %s id sequence: %w", table, err)
	}
	return nil
}

// The two readSQLite* functions below deliberately still say `contracts`
// and `entries`, plural, while every Postgres statement in this file says
// `contract` and `entry`. That is not an oversight. They read the legacy
// SQLite ledger, a frozen historical artifact whose schema was fixed years
// before the Postgres tables were renamed to singular (migration
// 00003_singular_table_names.sql) — renaming them here would simply fail
// against every real .db file this tool exists to read.

func readSQLiteContracts(db *sql.DB) ([]sqliteContract, error) {
	rows, err := db.Query(
		`SELECT id, name, number, home_resort, annual_points, use_year_month
		 FROM contracts ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []sqliteContract
	for rows.Next() {
		var c sqliteContract
		if err := rows.Scan(&c.ID, &c.Name, &c.Number, &c.HomeResort, &c.AnnualPoints, &c.UseYearMonth); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func readSQLiteEntries(db *sql.DB) ([]sqliteEntry, error) {
	rows, err := db.Query(
		`SELECT id, use_year, date, description, kind, allotted, used, tag, contract_id
		 FROM entries ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []sqliteEntry
	for rows.Next() {
		var e sqliteEntry
		if err := rows.Scan(&e.ID, &e.UseYear, &e.Date, &e.Description, &e.Kind, &e.Allotted, &e.Used, &e.Tag, &e.ContractID); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
