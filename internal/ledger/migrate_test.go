package ledger

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/pressly/goose/v3"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// execFixture runs a fixture SQL file (as a single multi-statement Exec,
// the same way the old schema.sql/seed.sql were applied) against db.
func execFixture(t *testing.T, db *sql.DB, path string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("execFixture: reading %s: %v", path, err)
	}
	if _, err := db.Exec(string(b)); err != nil {
		t.Fatalf("execFixture: executing %s: %v", path, err)
	}
}

// countRows is a small helper for "how many rows are in this table",
// used throughout this file to assert data survived (or didn't survive)
// a migration run.
func countRows(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatalf("countRows(%s): %v", table, err)
	}
	return n
}

// TestOpen_BaselineOverPreExistingDatabase is the rehearsal this whole
// commit exists for: it reproduces percival's live deployed database —
// tables and data created by the old re-exec-schema.sql-on-every-boot
// scheme, with no goose_db_version table — and proves that switching
// Store.Open over to goose does not lose or duplicate a single row of
// real data when it first runs against that database.
func TestOpen_BaselineOverPreExistingDatabase(t *testing.T) {
	dsn := OpenTestDSN(t)

	// Step 1: make the (isolated, schema-scoped) database look exactly
	// like percival's, by applying the frozen pre-goose fixtures the same
	// way the old Open did — two plain multi-statement Execs, no goose
	// involved at all yet.
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("opening admin connection: %v", err)
	}
	defer admin.Close()

	execFixture(t, admin, "testdata/legacy_schema.sql")
	execFixture(t, admin, "testdata/legacy_seed.sql")

	// Step 2: real data to lose, inserted directly (bypassing Store, which
	// doesn't exist yet in this test — the whole point is that no Store
	// has ever run against this database).
	//
	// Everything in this step still says `contracts`/`entries`/`dues_rates`,
	// plural, because at this point in the test that is genuinely what the
	// tables are called: this is percival's pre-goose schema, laid down from
	// the frozen testdata/legacy_schema.sql snapshot, and migration
	// 00003_singular_table_names.sql has not run yet. Every assertion after
	// Open() below uses the singular names, and the contrast is the point —
	// it proves the rename carried the real rows across rather than
	// quietly creating fresh empty tables beside them.
	var contractID int64
	if err := admin.QueryRow(
		`INSERT INTO contracts (name, number, home_resort, annual_points, use_year_month)
		 VALUES ('Point allocation', '1234567.000', 'VGF', 150, 4) RETURNING id`,
	).Scan(&contractID); err != nil {
		t.Fatalf("inserting contract: %v", err)
	}
	var entry1ID, entry2ID int64
	if err := admin.QueryRow(
		`INSERT INTO entries (use_year, date, description, kind, allotted, contract_id)
		 VALUES (2026, '2026-04-01', 'Point allocation', 'allocation', 150, $1) RETURNING id`,
		contractID,
	).Scan(&entry1ID); err != nil {
		t.Fatalf("inserting entry 1: %v", err)
	}
	if err := admin.QueryRow(
		`INSERT INTO entries (use_year, date, description, kind, used, contract_id)
		 VALUES (2026, '2026-06-04', 'BLT studio', 'usage', 40, $1) RETURNING id`,
		contractID,
	).Scan(&entry2ID); err != nil {
		t.Fatalf("inserting entry 2: %v", err)
	}

	if v := countRows(t, admin, "dues_rates"); v != 8 {
		t.Fatalf("dues_rates before Open = %d, want 8 (from legacy_seed.sql)", v)
	}

	// Sanity check: this really does look like percival — no
	// goose_db_version table exists yet. to_regclass resolves through
	// search_path (which OpenTestDSN scoped to this test's own isolated
	// schema) rather than information_schema.tables' unqualified
	// table_name, which matches across every schema in the database —
	// including the goose_db_version tables other tests' isolated schemas
	// create concurrently in this same physical database.
	var hasVersionTable bool
	if err := admin.QueryRow(`SELECT to_regclass('goose_db_version') IS NOT NULL`).Scan(&hasVersionTable); err != nil {
		t.Fatalf("checking for goose_db_version: %v", err)
	}
	if hasVersionTable {
		t.Fatal("goose_db_version already exists before the first ledger.Open — fixture setup is wrong")
	}

	// Step 3: the real thing under test — Open the pre-existing database
	// through the new goose-backed path.
	store, err := Open(dsn)
	if err != nil {
		t.Fatalf("Open against pre-existing database: %v", err)
	}
	defer store.Close()

	// The contract and both entries must still be exactly there, same ids.
	contracts, err := store.ListContracts(context.Background())
	if err != nil {
		t.Fatalf("ListContracts: %v", err)
	}
	if len(contracts) != 1 || contracts[0].ID != contractID {
		t.Fatalf("contracts after Open = %+v, want one row with id %d", contracts, contractID)
	}

	var e1Desc, e2Desc string
	if err := admin.QueryRow(`SELECT description FROM entry WHERE id = $1`, entry1ID).Scan(&e1Desc); err != nil {
		t.Fatalf("re-reading entry 1: %v", err)
	}
	if err := admin.QueryRow(`SELECT description FROM entry WHERE id = $1`, entry2ID).Scan(&e2Desc); err != nil {
		t.Fatalf("re-reading entry 2: %v", err)
	}
	if e1Desc != "Point allocation" || e2Desc != "BLT studio" {
		t.Fatalf("entries after Open: e1=%q e2=%q, want originals preserved", e1Desc, e2Desc)
	}
	if v := countRows(t, admin, "entry"); v != 2 {
		t.Fatalf("entries after Open = %d, want 2 (unchanged)", v)
	}

	// dues_rates must still have exactly 8 rows — the baseline migration's
	// seed step must recognize the pre-existing rows via its WHERE NOT
	// EXISTS guard and skip inserting, not duplicate them.
	if v := countRows(t, admin, "dues_rate"); v != 8 {
		t.Fatalf("dues_rates after Open = %d, want 8 (not duplicated)", v)
	}

	// All migrations must now be recorded as applied.
	version, err := goose.GetDBVersion(admin)
	if err != nil {
		t.Fatalf("goose.GetDBVersion: %v", err)
	}
	if version != 4 {
		t.Fatalf("goose db version after Open = %d, want 4 (all migrations applied)", version)
	}

	// Step 4: calling Open a second time must be a complete no-op.
	store2, err := Open(dsn)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer store2.Close()

	if v := countRows(t, admin, "contract"); v != 1 {
		t.Fatalf("contracts after second Open = %d, want 1 (unchanged)", v)
	}
	if v := countRows(t, admin, "entry"); v != 2 {
		t.Fatalf("entries after second Open = %d, want 2 (unchanged)", v)
	}
	if v := countRows(t, admin, "dues_rate"); v != 8 {
		t.Fatalf("dues_rates after second Open = %d, want 8 (unchanged)", v)
	}
	version2, err := goose.GetDBVersion(admin)
	if err != nil {
		t.Fatalf("goose.GetDBVersion after second Open: %v", err)
	}
	if version2 != 4 {
		t.Fatalf("goose db version after second Open = %d, want 4 (no new versions)", version2)
	}
	var versionRows int
	if err := admin.QueryRow(`SELECT count(*) FROM goose_db_version`).Scan(&versionRows); err != nil {
		t.Fatalf("counting goose_db_version rows: %v", err)
	}
	// goose records one bootstrap row (version 0) plus one row per applied
	// migration; a second Open must not add any more.
	const wantVersionRows = 5 // 0 (bootstrap), 1, 2, 3, 4
	if versionRows != wantVersionRows {
		t.Fatalf("goose_db_version row count after second Open = %d, want %d (no duplicate version rows)", versionRows, wantVersionRows)
	}
}

// TestOpen_FreshDatabase covers the other end of the spectrum from the
// baseline rehearsal above: an empty database (e.g. a brand-new local dev
// Postgres), where Open must create every table and record both
// migrations from scratch.
func TestOpen_FreshDatabase(t *testing.T) {
	dsn := OpenTestDSN(t)

	store, err := Open(dsn)
	if err != nil {
		t.Fatalf("Open against fresh database: %v", err)
	}
	defer store.Close()

	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("opening admin connection: %v", err)
	}
	defer admin.Close()

	if v := countRows(t, admin, "contract"); v != 0 {
		t.Fatalf("contracts on fresh database = %d, want 0", v)
	}
	if v := countRows(t, admin, "entry"); v != 0 {
		t.Fatalf("entries on fresh database = %d, want 0", v)
	}
	if v := countRows(t, admin, "dues_rate"); v != 8 {
		t.Fatalf("dues_rates on fresh database = %d, want 8 (seeded by 00002)", v)
	}

	version, err := goose.GetDBVersion(admin)
	if err != nil {
		t.Fatalf("goose.GetDBVersion: %v", err)
	}
	if version != 4 {
		t.Fatalf("goose db version on fresh database = %d, want 4", version)
	}

	// The rest of Store's API should work immediately, too.
	if _, err := store.AddContract(context.Background(), Contract{Name: "A", AnnualPoints: 120, UseYearMonth: time.April}); err != nil {
		t.Fatalf("AddContract on fresh database: %v", err)
	}
}

// TestOpen_SeededDuesSurviveDeletion pins the deliberate behaviour change
// documented in migrations/00002_seed_dues_rates.sql: because the seed now
// runs as a migration (applied at most once, ever, per goose_db_version)
// rather than as SQL re-executed on every boot, an operator who deletes
// every dues_rates row must see them stay deleted across a restart instead
// of being resurrected — matching what seed.sql's own comment always
// claimed but the old re-exec scheme did not actually deliver.
func TestOpen_SeededDuesSurviveDeletion(t *testing.T) {
	dsn := OpenTestDSN(t)

	store, err := Open(dsn)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if v, err := store.ListDuesRates(context.Background()); err != nil || len(v) != 8 {
		t.Fatalf("ListDuesRates after first Open = %v, %v; want 8 rows", v, err)
	}

	for _, y := range []int{2019, 2020, 2021, 2022, 2023, 2024, 2025, 2026} {
		if err := store.DeleteDuesRate(context.Background(), y); err != nil {
			t.Fatalf("DeleteDuesRate(%d): %v", y, err)
		}
	}
	if v, err := store.ListDuesRates(context.Background()); err != nil || len(v) != 0 {
		t.Fatalf("ListDuesRates after deleting all rows = %v, %v; want 0 rows", v, err)
	}
	store.Close()

	// Reopen — the old re-exec scheme would resurrect all 8 rows here.
	store2, err := Open(dsn)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer store2.Close()

	got, err := store2.ListDuesRates(context.Background())
	if err != nil {
		t.Fatalf("ListDuesRates after reopen: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("ListDuesRates after reopen = %v, want 0 (deletion must stick)", got)
	}
}

// TestSingularRename_DownUpRoundTrips proves 00003_singular_table_names.sql
// is genuinely reversible, which is the claim its Down block makes and the
// thing that separates it from the baseline's deliberately-inert Down.
//
// It also pins the property that actually matters about a rename migration:
// renaming carries the existing rows with it rather than creating fresh
// empty tables alongside them. A row is inserted before the round trip and
// must still be there, with the same id, after down-then-up.
func TestSingularRename_DownUpRoundTrips(t *testing.T) {
	dsn := OpenTestDSN(t)

	store, err := Open(dsn)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	id, err := store.AddContract(context.Background(), Contract{
		Name:         "Round trip",
		AnnualPoints: 150,
		UseYearMonth: time.April,
	})
	if err != nil {
		t.Fatalf("AddContract: %v", err)
	}
	store.Close()

	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("opening admin connection: %v", err)
	}
	defer admin.Close()

	if err := gooseSetup(); err != nil {
		t.Fatalf("gooseSetup: %v", err)
	}

	// Down to 2: the singular names must be gone and the plural ones back,
	// still holding the row inserted above.
	if err := goose.DownTo(admin, "migrations", 2); err != nil {
		t.Fatalf("goose.DownTo(2): %v", err)
	}
	if v := countRows(t, admin, "contracts"); v != 1 {
		t.Fatalf("contracts after Down = %d, want 1 (the rename must carry rows, not drop them)", v)
	}
	if to := regclass(t, admin, "contract"); to {
		t.Fatal("table `contract` still exists after Down — the Down block did not rename it back")
	}

	// Up again: back to singular, same row, same id.
	if err := goose.Up(admin, "migrations"); err != nil {
		t.Fatalf("goose.Up: %v", err)
	}
	var gotID int64
	if err := admin.QueryRow(`SELECT id FROM contract WHERE name = 'Round trip'`).Scan(&gotID); err != nil {
		t.Fatalf("re-reading contract after Up: %v", err)
	}
	if gotID != id {
		t.Fatalf("contract id after round trip = %d, want %d (ids must survive a rename)", gotID, id)
	}
	if to := regclass(t, admin, "contracts"); to {
		t.Fatal("table `contracts` still exists after Up — the Up block did not rename it")
	}
}

// TestTripMigration_DownUpRoundTrips proves 00004_trip.sql's Down block is
// wired correctly: unlike the singular-rename migration, trip/trip_stay are
// dropped outright on Down (there is no data to carry across a DROP TABLE),
// so this pins that Down removes both tables and Up recreates them empty,
// rather than pinning row survival the way the rename test does.
func TestTripMigration_DownUpRoundTrips(t *testing.T) {
	dsn := OpenTestDSN(t)

	store, err := Open(dsn)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := store.AddTrip(context.Background(), Trip{
		Name:      "Round trip",
		StartDate: time.Date(2026, time.June, 1, 0, 0, 0, 0, time.UTC),
		EndDate:   time.Date(2026, time.June, 8, 0, 0, 0, 0, time.UTC),
		MinNights: 4,
	}); err != nil {
		t.Fatalf("AddTrip: %v", err)
	}
	store.Close()

	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("opening admin connection: %v", err)
	}
	defer admin.Close()

	if err := gooseSetup(); err != nil {
		t.Fatalf("gooseSetup: %v", err)
	}

	// Down to 3: both trip tables must be gone.
	if err := goose.DownTo(admin, "migrations", 3); err != nil {
		t.Fatalf("goose.DownTo(3): %v", err)
	}
	if regclass(t, admin, "trip_stay") {
		t.Fatal("table `trip_stay` still exists after Down — the Down block did not drop it")
	}
	if regclass(t, admin, "trip") {
		t.Fatal("table `trip` still exists after Down — the Down block did not drop it")
	}

	// Up again: both tables recreated, empty.
	if err := goose.Up(admin, "migrations"); err != nil {
		t.Fatalf("goose.Up: %v", err)
	}
	if !regclass(t, admin, "trip") {
		t.Fatal("table `trip` does not exist after Up")
	}
	if !regclass(t, admin, "trip_stay") {
		t.Fatal("table `trip_stay` does not exist after Up")
	}
	if v := countRows(t, admin, "trip"); v != 0 {
		t.Fatalf("trip rows after Up = %d, want 0 (Down dropped the table, taking the earlier row with it)", v)
	}
}

// regclass reports whether table resolves in the current search_path,
// which OpenTestDSN has scoped to this test's own isolated schema. It is
// used instead of information_schema.tables for the reason spelled out in
// TestOpen_BaselineOverPreExistingDatabase: that view matches on an
// unqualified table_name across every schema in the database, so it
// false-positives against other tests running concurrently.
func regclass(t *testing.T, db *sql.DB, table string) bool {
	t.Helper()
	var exists bool
	if err := db.QueryRow(`SELECT to_regclass($1) IS NOT NULL`, table).Scan(&exists); err != nil {
		t.Fatalf("to_regclass(%s): %v", table, err)
	}
	return exists
}
