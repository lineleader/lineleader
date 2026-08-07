// Command ledger-migrate is a one-shot copy of the legacy SQLite points
// ledger (~/.config/lineleader/ledger.db) into the hosted Postgres ledger,
// preserving contract and entry ids — and the entries.contract_id foreign
// key — exactly. It is standalone on purpose: the dvc CLI must not link a
// database driver, so this command, and only this command, links both
// modernc.org/sqlite and pgx.
//
// It refuses to run against a Postgres database that already has ledger
// rows: this is a migration, not a sync. The old ledger.db is left on disk
// untouched, so it doubles as the rollback path if the cutover needs to be
// undone.
//
// Usage (typical one-shot run against the home-lab Postgres):
//
//	go run ./cmd/ledger-migrate \
//	    --sqlite ~/.config/lineleader/ledger.db \
//	    --dsn "$LEDGER_DSN"
//
// Both flags have defaults: --sqlite mirrors the pre-Postgres-port
// ledger.DefaultLedgerPath ($HOME/.config/lineleader/ledger.db via
// os.UserConfigDir), and --dsn falls back to the LEDGER_DSN environment
// variable already used by cmd/server and dvc ledger.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	sqlitePath := flag.String("sqlite", defaultSQLitePath(), "path to the legacy SQLite ledger.db")
	dsn := flag.String("dsn", os.Getenv("LEDGER_DSN"), "target Postgres DSN (or set LEDGER_DSN)")
	flag.Parse()

	if *dsn == "" {
		fmt.Fprintln(os.Stderr, "ledger-migrate: no target DSN provided (use --dsn or set LEDGER_DSN)")
		os.Exit(1)
	}

	summary, err := Migrate(*sqlitePath, *dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ledger-migrate: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("migrated %d contract(s), %d entries from %s\n", summary.Contracts, summary.Entries, *sqlitePath)
}

// defaultSQLitePath mirrors the old ledger.DefaultLedgerPath (removed from
// internal/ledger when the store moved to Postgres): $HOME/.config/lineleader/ledger.db
// via os.UserConfigDir, falling back to $HOME if that's unavailable.
func defaultSQLitePath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = os.Getenv("HOME")
	}
	return filepath.Join(dir, "lineleader", "ledger.db")
}
