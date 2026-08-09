package ledger

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

//go:embed schema.sql
var schema string

//go:embed seed.sql
var seed string

// Store is a Postgres-backed handle to the points ledger.
type Store struct {
	db *sql.DB
}

// Open opens a connection pool to the Postgres database identified by dsn,
// applies the schema, then seeds reference data. Both steps are idempotent
// (CREATE TABLE / INDEX IF NOT EXISTS for the schema; a whole-table
// WHERE NOT EXISTS guard for the seed), so it is safe to call on every
// process start.
func Open(dsn string) (*Store, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("connecting to ledger database: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("applying schema: %w", err)
	}
	if _, err := db.Exec(seed); err != nil {
		db.Close()
		return nil, fmt.Errorf("seeding reference data: %w", err)
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
