package main

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/lineleader/lineleader/internal/ledger"

	_ "modernc.org/sqlite"
)

// legacySchema mirrors the pre-Postgres-port internal/ledger/schema.sql
// (SQLite dialect: INTEGER PRIMARY KEY AUTOINCREMENT ids).
const legacySchema = `
CREATE TABLE contracts (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    name           TEXT    NOT NULL,
    number         TEXT    NOT NULL DEFAULT '',
    home_resort    TEXT    NOT NULL DEFAULT '',
    annual_points  INTEGER NOT NULL DEFAULT 0,
    use_year_month INTEGER NOT NULL DEFAULT 1
);

CREATE TABLE entries (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    use_year    INTEGER NOT NULL,
    date        TEXT    NOT NULL,
    description TEXT    NOT NULL DEFAULT '',
    kind        TEXT    NOT NULL,
    allotted    INTEGER NOT NULL DEFAULT 0,
    used        INTEGER NOT NULL DEFAULT 0,
    tag         TEXT    NOT NULL DEFAULT '',
    contract_id INTEGER REFERENCES contracts(id) ON DELETE SET NULL
);
`

// newFixtureSQLite builds a temp SQLite ledger.db seeded with rows chosen to
// exercise every migration edge case: ids that don't start at 1, a gap in a
// contract's entry ids, one entry with a NULL contract_id, and one entry
// linked to a contract.
func newFixtureSQLite(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ledger.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("opening fixture sqlite db: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec(legacySchema); err != nil {
		t.Fatalf("creating fixture schema: %v", err)
	}

	if _, err := db.Exec(
		`INSERT INTO contracts (id, name, number, home_resort, annual_points, use_year_month)
		 VALUES (7, 'VGF Contract', '1234567.000', 'VGF', 150, 4)`,
	); err != nil {
		t.Fatalf("seeding contract: %v", err)
	}

	entries := []struct {
		id         int64
		useYear    int
		date       string
		desc       string
		kind       string
		allotted   int
		used       int
		tag        string
		contractID any // int64 or nil
	}{
		// id 100 and 110 belong to contract 7 — a gap (105 belongs to no contract).
		{100, 2026, "2026-04-01", "Point allocation", "allocation", 150, 0, "", int64(7)},
		{105, 2026, "2026-05-10", "Trip to WDW", "usage", 0, 40, "", nil},
		{110, 2026, "2026-06-01", "Bonus points", "bonus", 25, 0, "", int64(7)},
	}
	for _, e := range entries {
		if _, err := db.Exec(
			`INSERT INTO entries (id, use_year, date, description, kind, allotted, used, tag, contract_id)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			e.id, e.useYear, e.date, e.desc, e.kind, e.allotted, e.used, e.tag, e.contractID,
		); err != nil {
			t.Fatalf("seeding entry %d: %v", e.id, err)
		}
	}

	return path
}

func TestMigrate(t *testing.T) {
	sqlitePath := newFixtureSQLite(t)
	dsn := ledger.OpenTestDSN(t)

	summary, err := Migrate(sqlitePath, dsn)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if summary.Contracts != 1 {
		t.Errorf("summary.Contracts = %d, want 1", summary.Contracts)
	}
	if summary.Entries != 3 {
		t.Errorf("summary.Entries = %d, want 3", summary.Entries)
	}

	store, err := ledger.Open(dsn)
	if err != nil {
		t.Fatalf("ledger.Open after migrate: %v", err)
	}
	defer store.Close()

	contracts, err := store.ListContracts()
	if err != nil {
		t.Fatalf("ListContracts: %v", err)
	}
	if len(contracts) != 1 {
		t.Fatalf("ListContracts len = %d, want 1", len(contracts))
	}
	if contracts[0].ID != 7 {
		t.Errorf("contract id = %d, want 7 (preserved)", contracts[0].ID)
	}
	if contracts[0].Name != "VGF Contract" || contracts[0].AnnualPoints != 150 {
		t.Errorf("contract fields not preserved: %+v", contracts[0])
	}

	entries, err := store.ListEntries()
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("ListEntries len = %d, want 3", len(entries))
	}
	// ordered by (date, id): 100, 105, 110
	wantIDs := []int64{100, 105, 110}
	for i, e := range entries {
		if e.ID != wantIDs[i] {
			t.Errorf("entries[%d].ID = %d, want %d (ids must be preserved)", i, e.ID, wantIDs[i])
		}
	}
	if entries[0].ContractID == nil || *entries[0].ContractID != 7 {
		t.Errorf("entries[0].ContractID = %v, want *7 (FK preserved)", entries[0].ContractID)
	}
	if entries[1].ContractID != nil {
		t.Errorf("entries[1].ContractID = %v, want nil", entries[1].ContractID)
	}
	if entries[2].ContractID == nil || *entries[2].ContractID != 7 {
		t.Errorf("entries[2].ContractID = %v, want *7 (FK preserved)", entries[2].ContractID)
	}

	// Sequence-reset proof: the next generated id must be max+1, not a
	// collision with a migrated row.
	newContractID, err := store.AddContract(ledger.Contract{Name: "New", AnnualPoints: 100, UseYearMonth: 1})
	if err != nil {
		t.Fatalf("AddContract after migrate: %v", err)
	}
	if newContractID != 8 {
		t.Errorf("new contract id = %d, want 8 (sequence reset to max+1)", newContractID)
	}

	newEntryID, err := store.AddEntry(ledger.Entry{UseYear: 2026, Date: entries[0].Date, Desc: "New", Kind: ledger.KindUsage})
	if err != nil {
		t.Fatalf("AddEntry after migrate: %v", err)
	}
	if newEntryID != 111 {
		t.Errorf("new entry id = %d, want 111 (sequence reset to max+1)", newEntryID)
	}
}

func TestMigrateRefusesNonEmptyTarget(t *testing.T) {
	sqlitePath := newFixtureSQLite(t)
	dsn := ledger.OpenTestDSN(t)

	if _, err := Migrate(sqlitePath, dsn); err != nil {
		t.Fatalf("first Migrate: %v", err)
	}

	if _, err := Migrate(sqlitePath, dsn); err == nil {
		t.Fatal("second Migrate on a non-empty target: want error, got nil")
	}
}
