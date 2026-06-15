package ledger

import (
	"database/sql"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schema string

// Store is a SQLite-backed handle to the points ledger.
type Store struct {
	db *sql.DB
}

// DefaultLedgerPath returns the platform-appropriate path for the ledger database,
// mirroring dvc.DefaultConfigPath.
func DefaultLedgerPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = os.Getenv("HOME")
	}
	return filepath.Join(dir, "lineleader", "ledger.db")
}

// Open opens (creating if needed) the ledger database at path and applies the schema.
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("creating ledger dir: %w", err)
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		db.Close()
		return nil, err
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

// AddContract inserts c and returns its new id.
func (s *Store) AddContract(c Contract) (int64, error) {
	res, err := s.db.Exec(
		`INSERT INTO contracts (name, number, home_resort, annual_points, use_year_month)
		 VALUES (?, ?, ?, ?, ?)`,
		c.Name, c.Number, c.HomeResort, c.AnnualPoints, int(c.UseYearMonth),
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
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
		 SET name = ?, number = ?, home_resort = ?, annual_points = ?, use_year_month = ?
		 WHERE id = ?`,
		c.Name, c.Number, c.HomeResort, c.AnnualPoints, int(c.UseYearMonth), c.ID,
	)
	return err
}

// DeleteContract removes the contract with the given id.
func (s *Store) DeleteContract(id int64) error {
	_, err := s.db.Exec(`DELETE FROM contracts WHERE id = ?`, id)
	return err
}
