package ledger

// DuesRate is one year's global $/pt dues rate. Dues are a single series
// shared by every contract, not per-contract data.
type DuesRate struct {
	UseYear int
	Rate    Micros
}

// ListDuesRates returns every stored dues rate ordered by use year ascending.
func (s *Store) ListDuesRates() ([]DuesRate, error) {
	rows, err := s.db.Query(`SELECT use_year, rate_micros FROM dues_rates ORDER BY use_year`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []DuesRate
	for rows.Next() {
		var (
			d      DuesRate
			micros int64
		)
		if err := rows.Scan(&d.UseYear, &micros); err != nil {
			return nil, err
		}
		d.Rate = Micros(micros)
		out = append(out, d)
	}
	return out, rows.Err()
}

// UpsertDuesRate inserts d.UseYear's rate, or overwrites it if that year is
// already stored.
func (s *Store) UpsertDuesRate(d DuesRate) error {
	_, err := s.db.Exec(
		`INSERT INTO dues_rates (use_year, rate_micros) VALUES ($1, $2)
		 ON CONFLICT (use_year) DO UPDATE SET rate_micros = EXCLUDED.rate_micros`,
		d.UseYear, int64(d.Rate),
	)
	return err
}

// DeleteDuesRate removes the stored rate for useYear, if any. Because
// seed.sql only seeds when dues_rates is entirely empty (a table-level
// guard, not per-row ON CONFLICT), a deleted year never reappears on a
// later restart.
func (s *Store) DeleteDuesRate(useYear int) error {
	_, err := s.db.Exec(`DELETE FROM dues_rates WHERE use_year = $1`, useYear)
	return err
}
