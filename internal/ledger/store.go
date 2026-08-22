package ledger

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"sync"
	"time"

	"github.com/pressly/goose/v3"

	"github.com/lineleader/lineleader/internal/ledger/dbgen"

	_ "github.com/jackc/pgx/v5/stdlib"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// gooseSetupOnce guards goose.SetBaseFS / goose.SetDialect / goose.SetLogger,
// which are package-level state in the goose library, not per-call
// arguments. Tests in this package open many Stores concurrently
// (t.Parallel), and every one of them calls Open — without this, concurrent
// calls to the setup functions would race on goose's internal globals. The
// setup itself is idempotent (same FS, same dialect, every time), so
// running it once for the life of the process is correct, not just
// convenient.
var (
	gooseSetupOnce sync.Once
	gooseSetupErr  error
)

func gooseSetup() error {
	gooseSetupOnce.Do(func() {
		goose.SetBaseFS(&migrationsFS)
		// goose logs each applied migration to stdout by default. This app
		// logs via the standard `log` package everywhere else, and a
		// migration run happens on every boot (and in every test that
		// calls Open) — left at its default, goose's own logger would add
		// a second, differently-formatted stream of boot noise. Silence it
		// here; Up's returned error (wrapped below) is still surfaced to
		// the caller, so failures are not swallowed, only the routine
		// per-migration chatter is.
		goose.SetLogger(goose.NopLogger())
		if err := goose.SetDialect("postgres"); err != nil {
			gooseSetupErr = fmt.Errorf("configuring goose dialect: %w", err)
		}
	})
	return gooseSetupErr
}

// Store is a Postgres-backed handle to the points ledger. Every query runs
// through dbgen, the sqlc-generated layer over the schema in migrations/
// (see sqlc.yaml and `make sqlc`); this file and entries.go/dues.go/
// distribute.go hold the mapping functions between dbgen's generated
// models and this package's own domain types (Contract, Entry, ...), which
// carry derived fields (e.g. Entry.RunningBalance) sqlc cannot model.
type Store struct {
	db *sql.DB
	q  *dbgen.Queries
}

// Open opens a connection pool to the Postgres database identified by dsn
// and brings its schema up to date by running every not-yet-applied
// migration under migrations/ (embedded at build time) via goose. This
// replaces the old scheme of re-executing schema.sql/seed.sql as
// idempotent SQL on every process start: goose tracks applied migrations
// in a goose_db_version table, so each migration now runs at most once
// ever, rather than on every boot. It is still safe to call Open on every
// process start — goose.Up is a no-op once every migration has already
// been applied — including against percival's live, already-populated
// database, whose baseline migration (00001_initial.sql) is written to
// adopt that existing schema rather than recreate it.
func Open(dsn string) (*Store, error) {
	if err := gooseSetup(); err != nil {
		return nil, err
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("connecting to ledger database: %w", err)
	}
	if err := goose.Up(db, "migrations"); err != nil {
		db.Close()
		return nil, fmt.Errorf("applying migrations: %w", err)
	}
	return &Store{db: db, q: dbgen.New(db)}, nil
}

// WithTx returns a Store whose queries run inside tx instead of against the
// pool directly, for callers (e.g. the upcoming Trips work) that need
// several writes to commit atomically. It shares the receiver's db handle
// — only the query executor changes, via sqlc's generated
// (*dbgen.Queries).WithTx.
func (s *Store) WithTx(tx *sql.Tx) *Store {
	return &Store{db: s.db, q: s.q.WithTx(tx)}
}

// Close releases the database handle.
func (s *Store) Close() error { return s.db.Close() }

// Ping verifies the database connection is alive. Used by the /healthz
// endpoint for reverse-proxy liveness checks.
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// parseDate reads a stored date, tolerating either a bare date or a full RFC3339
// timestamp (some tooling may write the latter).
func parseDate(s string) (time.Time, error) {
	if t, err := time.Parse(DateLayout, s); err == nil {
		return t, nil
	}
	return time.Parse(time.RFC3339, s)
}

// contractFromRow maps a dbgen.Contract (sqlc's generated model, one field
// per schema column) onto the domain Contract type: use_year_month is
// stored as a plain INTEGER and converted to time.Month here, and
// purchase_price_cents/closing_costs_cents to Cents.
func contractFromRow(row dbgen.Contract) Contract {
	return Contract{
		ID:            row.ID,
		Name:          row.Name,
		Number:        row.Number,
		HomeResort:    row.HomeResort,
		AnnualPoints:  int(row.AnnualPoints),
		UseYearMonth:  time.Month(row.UseYearMonth),
		TermYears:     int(row.TermYears),
		PurchasePrice: Cents(row.PurchasePriceCents),
		ClosingCosts:  Cents(row.ClosingCostsCents),
	}
}

// Every Store method in this file, and in entries.go/dues.go/distribute.go,
// calls its dbgen.Queries method with context.Background() rather than a
// real context: no exported Store method takes a context.Context today
// (Ping above is the one deliberate exception, predating sqlc). That is a
// known, temporary gap — issue lineleader-tip tracks threading real
// contexts through every exported method — not something to patch
// site-by-site here, so there is deliberately no TODO on each of the 14
// call sites this comment covers.

// AddContract inserts c and returns its new id.
func (s *Store) AddContract(c Contract) (int64, error) {
	return s.q.InsertContract(context.Background(), dbgen.InsertContractParams{
		Name:               c.Name,
		Number:             c.Number,
		HomeResort:         c.HomeResort,
		AnnualPoints:       int32(c.AnnualPoints),
		UseYearMonth:       int32(c.UseYearMonth),
		TermYears:          int32(c.TermYears),
		PurchasePriceCents: int64(c.PurchasePrice),
		ClosingCostsCents:  int64(c.ClosingCosts),
	})
}

// ListContracts returns all contracts ordered by id.
func (s *Store) ListContracts() ([]Contract, error) {
	rows, err := s.q.ListContracts(context.Background())
	if err != nil {
		return nil, err
	}
	var out []Contract
	for _, row := range rows {
		out = append(out, contractFromRow(row))
	}
	return out, nil
}

// UpdateContract overwrites the contract identified by c.ID.
func (s *Store) UpdateContract(c Contract) error {
	return s.q.UpdateContract(context.Background(), dbgen.UpdateContractParams{
		Name:               c.Name,
		Number:             c.Number,
		HomeResort:         c.HomeResort,
		AnnualPoints:       int32(c.AnnualPoints),
		UseYearMonth:       int32(c.UseYearMonth),
		TermYears:          int32(c.TermYears),
		PurchasePriceCents: int64(c.PurchasePrice),
		ClosingCostsCents:  int64(c.ClosingCosts),
		ID:                 c.ID,
	})
}

// DeleteContract removes the contract with the given id.
func (s *Store) DeleteContract(id int64) error {
	return s.q.DeleteContract(context.Background(), id)
}
