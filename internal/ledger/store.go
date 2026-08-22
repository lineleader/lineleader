package ledger

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"sync"
	"time"

	"github.com/pressly/goose/v3"

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

// Store is a Postgres-backed handle to the points ledger.
type Store struct {
	db *sql.DB
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
	return &Store{db: db}, nil
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

// AddContract inserts c and returns its new id.
func (s *Store) AddContract(c Contract) (int64, error) {
	var id int64
	err := s.db.QueryRow(
		`INSERT INTO contracts (name, number, home_resort, annual_points, use_year_month, term_years, purchase_price_cents, closing_costs_cents)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`,
		c.Name, c.Number, c.HomeResort, c.AnnualPoints, int(c.UseYearMonth), c.TermYears, int64(c.PurchasePrice), int64(c.ClosingCosts),
	).Scan(&id)
	if err != nil {
		return 0, err
	}
	return id, nil
}

// ListContracts returns all contracts ordered by id.
func (s *Store) ListContracts() ([]Contract, error) {
	rows, err := s.db.Query(
		`SELECT id, name, number, home_resort, annual_points, use_year_month, term_years, purchase_price_cents, closing_costs_cents
		 FROM contracts ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Contract
	for rows.Next() {
		var (
			c              Contract
			month          int
			purchase, cost int64
		)
		if err := rows.Scan(&c.ID, &c.Name, &c.Number, &c.HomeResort, &c.AnnualPoints, &month, &c.TermYears, &purchase, &cost); err != nil {
			return nil, err
		}
		c.UseYearMonth = time.Month(month)
		c.PurchasePrice = Cents(purchase)
		c.ClosingCosts = Cents(cost)
		out = append(out, c)
	}
	return out, rows.Err()
}

// UpdateContract overwrites the contract identified by c.ID.
func (s *Store) UpdateContract(c Contract) error {
	_, err := s.db.Exec(
		`UPDATE contracts
		 SET name = $1, number = $2, home_resort = $3, annual_points = $4, use_year_month = $5,
		     term_years = $6, purchase_price_cents = $7, closing_costs_cents = $8
		 WHERE id = $9`,
		c.Name, c.Number, c.HomeResort, c.AnnualPoints, int(c.UseYearMonth), c.TermYears, int64(c.PurchasePrice), int64(c.ClosingCosts), c.ID,
	)
	return err
}

// DeleteContract removes the contract with the given id.
func (s *Store) DeleteContract(id int64) error {
	_, err := s.db.Exec(`DELETE FROM contracts WHERE id = $1`, id)
	return err
}
