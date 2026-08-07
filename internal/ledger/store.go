package ledger

import (
	"database/sql"
	_ "embed"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

//go:embed schema.sql
var schema string

// Store is a Postgres-backed handle to the points ledger.
type Store struct {
	db *sql.DB
}

// Open opens a connection pool to the Postgres database identified by dsn
// and applies the schema. Schema application is idempotent (CREATE TABLE /
// INDEX IF NOT EXISTS), so it is safe to call on every process start.
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
	return &Store{db: db}, nil
}

// Close releases the database handle.
func (s *Store) Close() error { return s.db.Close() }

const dateLayout = "2006-01-02"

// parseDate reads a stored date, tolerating either a bare date or a full RFC3339
// timestamp (some tooling may write the latter).
func parseDate(s string) (time.Time, error) {
	if t, err := time.Parse(dateLayout, s); err == nil {
		return t, nil
	}
	return time.Parse(time.RFC3339, s)
}

// AddContract inserts c and returns its new id.
func (s *Store) AddContract(c Contract) (int64, error) {
	var id int64
	err := s.db.QueryRow(
		`INSERT INTO contracts (name, number, home_resort, annual_points, use_year_month)
		 VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		c.Name, c.Number, c.HomeResort, c.AnnualPoints, int(c.UseYearMonth),
	).Scan(&id)
	if err != nil {
		return 0, err
	}
	return id, nil
}

// ListContracts returns all contracts ordered by id.
func (s *Store) ListContracts() ([]Contract, error) {
	rows, err := s.db.Query(
		`SELECT id, name, number, home_resort, annual_points, use_year_month
		 FROM contracts ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Contract
	for rows.Next() {
		var c Contract
		var month int
		if err := rows.Scan(&c.ID, &c.Name, &c.Number, &c.HomeResort, &c.AnnualPoints, &month); err != nil {
			return nil, err
		}
		c.UseYearMonth = time.Month(month)
		out = append(out, c)
	}
	return out, rows.Err()
}

// UpdateContract overwrites the contract identified by c.ID.
func (s *Store) UpdateContract(c Contract) error {
	_, err := s.db.Exec(
		`UPDATE contracts
		 SET name = $1, number = $2, home_resort = $3, annual_points = $4, use_year_month = $5
		 WHERE id = $6`,
		c.Name, c.Number, c.HomeResort, c.AnnualPoints, int(c.UseYearMonth), c.ID,
	)
	return err
}

// DeleteContract removes the contract with the given id.
func (s *Store) DeleteContract(id int64) error {
	_, err := s.db.Exec(`DELETE FROM contracts WHERE id = $1`, id)
	return err
}
