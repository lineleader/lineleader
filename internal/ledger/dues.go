package ledger

import (
	"context"
	"fmt"

	"github.com/lineleader/lineleader/internal/ledger/dbgen"
)

// DuesRate is one year's global $/pt dues rate. Dues are a single series
// shared by every contract, not per-contract data.
type DuesRate struct {
	UseYear int
	Rate    Micros
}

// duesRateFromRow maps a dbgen.DuesRate (sqlc's generated model) onto the
// domain DuesRate type.
func duesRateFromRow(row dbgen.DuesRate) DuesRate {
	return DuesRate{UseYear: int(row.UseYear), Rate: Micros(row.RateMicros)}
}

// ListDuesRates returns every stored dues rate ordered by use year ascending.
func (s *Store) ListDuesRates(ctx context.Context) ([]DuesRate, error) {
	rows, err := s.q.ListDuesRates(ctx)
	if err != nil {
		return nil, err
	}
	var out []DuesRate
	for _, row := range rows {
		out = append(out, duesRateFromRow(row))
	}
	return out, nil
}

// UpsertDuesRate inserts d.UseYear's rate, or overwrites it if that year is
// already stored.
//
// d.Rate must be positive: a dues rate of 0 or less is nonsense (and,
// unfiltered, would eventually reach a division in duesGrowth or
// compoundDues and panic — see NewCostBasis's belt-and-braces filter for the
// other side of that defense). d.UseYear only gets a loose sanity check — a
// four-digit calendar year — since this table stores an open-ended series
// and there's no real reason to hard-code a narrower operational range.
func (s *Store) UpsertDuesRate(ctx context.Context, d DuesRate) error {
	if d.Rate <= 0 {
		return fmt.Errorf("UpsertDuesRate: rate must be positive, got %v", d.Rate)
	}
	if d.UseYear < 1000 || d.UseYear > 9999 {
		return fmt.Errorf("UpsertDuesRate: use year %d is not a plausible calendar year", d.UseYear)
	}
	return s.q.UpsertDuesRate(ctx, dbgen.UpsertDuesRateParams{
		UseYear:    int32(d.UseYear),
		RateMicros: int64(d.Rate),
	})
}

// DeleteDuesRate removes the stored rate for useYear, if any. Because
// seed.sql only seeds when dues_rates is entirely empty (a table-level
// guard, not per-row ON CONFLICT), a deleted year never reappears on a
// later restart.
func (s *Store) DeleteDuesRate(ctx context.Context, useYear int) error {
	return s.q.DeleteDuesRate(ctx, int32(useYear))
}
