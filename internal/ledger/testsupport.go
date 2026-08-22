package ledger

import (
	"database/sql"
	"fmt"
	"math/rand"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
)

var schemaNameDisallowed = regexp.MustCompile(`[^a-z0-9_]+`)

// OpenTestDSN prepares a uniquely named, isolated Postgres schema for a test
// and returns a DSN scoped to it (via a search_path override). The schema is
// dropped (CASCADE) via t.Cleanup.
//
// It reads the connection DSN from the LEDGER_TEST_DSN environment variable
// and skips the test cleanly (not a failure) when that variable is unset —
// run `make test-db` for a throwaway local Postgres and export the DSN it
// prints.
func OpenTestDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("LEDGER_TEST_DSN")
	if dsn == "" {
		t.Skip("LEDGER_TEST_DSN not set; run `make test-db`, export the DSN it prints, then re-run tests")
	}

	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("ledger.OpenTestDSN: opening admin connection: %v", err)
	}
	t.Cleanup(func() { _ = admin.Close() })

	schemaName := testSchemaName(t)
	if _, err := admin.Exec(fmt.Sprintf(`CREATE SCHEMA %q`, schemaName)); err != nil {
		t.Fatalf("ledger.OpenTestDSN: creating schema %s: %v", schemaName, err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(fmt.Sprintf(`DROP SCHEMA IF EXISTS %q CASCADE`, schemaName)); err != nil {
			t.Errorf("ledger.OpenTestDSN: dropping schema %s: %v", schemaName, err)
		}
	})

	scopedDSN, err := scopeDSNToSchema(dsn, schemaName)
	if err != nil {
		t.Fatalf("ledger.OpenTestDSN: building scoped DSN: %v", err)
	}
	return scopedDSN
}

// OpenTest is OpenTestDSN followed by Open: it returns a ready-to-use Store
// backed by a fresh, isolated schema, closed automatically via t.Cleanup.
func OpenTest(t *testing.T) *Store {
	t.Helper()
	dsn := OpenTestDSN(t)
	store, err := Open(dsn)
	if err != nil {
		t.Fatalf("ledger.OpenTest: Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// testSchemaName derives a Postgres-identifier-safe, collision-resistant
// schema name from the test name plus a random suffix (subtests and
// t.Parallel siblings can otherwise share a name).
func testSchemaName(t *testing.T) string {
	base := "ll_test_" + schemaNameDisallowed.ReplaceAllString(strings.ToLower(t.Name()), "_")
	suffix := fmt.Sprintf("_%x", rand.Int63())
	const maxIdentifier = 63
	if len(base)+len(suffix) > maxIdentifier {
		base = base[:maxIdentifier-len(suffix)]
	}
	return base + suffix
}

// scopeDSNToSchema appends (or overwrites) a search_path query parameter so
// that unqualified table references in the migrations under migrations/ —
// and every subsequent query, including goose's own goose_db_version
// bookkeeping table — resolve to the given schema.
func scopeDSNToSchema(dsn, schemaName string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("parsing DSN: %w", err)
	}
	q := u.Query()
	q.Set("search_path", schemaName)
	u.RawQuery = q.Encode()
	return u.String(), nil
}
