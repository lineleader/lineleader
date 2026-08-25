package ledger

import (
	"context"
	"errors"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	return OpenTest(t)
}

func TestUseYearForDate(t *testing.T) {
	tests := []struct {
		date  string
		start time.Month
		want  int
	}{
		{"2026-02-15", time.April, 2025}, // before the April start → prior use year
		{"2026-04-01", time.April, 2026}, // on the start month → current use year
		{"2026-12-31", time.April, 2026},
		{"2026-08-10", time.August, 2026},
		{"2026-07-31", time.August, 2025},
		{"2026-06-13", time.January, 2026}, // January start tracks the calendar year
	}
	for _, tc := range tests {
		d, err := time.Parse("2006-01-02", tc.date)
		if err != nil {
			t.Fatal(err)
		}
		if got := UseYearForDate(d, tc.start); got != tc.want {
			t.Errorf("UseYearForDate(%s, %s) = %d, want %d", tc.date, tc.start, got, tc.want)
		}
	}
}

func TestContractCRUD(t *testing.T) {
	s := openTestStore(t)

	c := Contract{
		Name:         "Point allocation #2",
		Number:       "1234567.000",
		HomeResort:   "VGF",
		AnnualPoints: 150,
		UseYearMonth: time.April,
	}
	id, err := s.AddContract(context.Background(), c)
	if err != nil {
		t.Fatalf("AddContract: %v", err)
	}
	if id == 0 {
		t.Fatal("AddContract returned id 0")
	}

	got, err := s.ListContracts(context.Background())
	if err != nil {
		t.Fatalf("ListContracts: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ListContracts len = %d, want 1", len(got))
	}
	c.ID = id
	if got[0] != c {
		t.Errorf("ListContracts[0] = %+v, want %+v", got[0], c)
	}

	c.ID = id
	c.AnnualPoints = 160
	c.Number = "7654321.000"
	if err := s.UpdateContract(context.Background(), c); err != nil {
		t.Fatalf("UpdateContract: %v", err)
	}
	got, _ = s.ListContracts(context.Background())
	if got[0].AnnualPoints != 160 || got[0].Number != "7654321.000" {
		t.Errorf("after update = %+v, want points=160 number=7654321.000", got[0])
	}

	if err := s.DeleteContract(context.Background(), id); err != nil {
		t.Fatalf("DeleteContract: %v", err)
	}
	got, _ = s.ListContracts(context.Background())
	if len(got) != 0 {
		t.Fatalf("after delete len = %d, want 0", len(got))
	}
}

func TestContractCostFields(t *testing.T) {
	s := openTestStore(t)

	c := Contract{
		Name:          "Point allocation",
		Number:        "9876543.000",
		HomeResort:    "AKV",
		AnnualPoints:  120,
		UseYearMonth:  time.April,
		TermYears:     44,
		PurchasePrice: 2_940_000, // $29,400.00
		ClosingCosts:  58_835,    // $588.35
	}
	id, err := s.AddContract(context.Background(), c)
	if err != nil {
		t.Fatalf("AddContract: %v", err)
	}

	got, err := s.ListContracts(context.Background())
	if err != nil {
		t.Fatalf("ListContracts: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ListContracts len = %d, want 1", len(got))
	}
	c.ID = id
	if got[0] != c {
		t.Errorf("ListContracts[0] = %+v, want %+v", got[0], c)
	}

	c.TermYears = 41
	c.PurchasePrice = 3_015_000
	c.ClosingCosts = 66_500
	if err := s.UpdateContract(context.Background(), c); err != nil {
		t.Fatalf("UpdateContract: %v", err)
	}
	got, _ = s.ListContracts(context.Background())
	if got[0].TermYears != 41 || got[0].PurchasePrice != 3_015_000 || got[0].ClosingCosts != 66_500 {
		t.Errorf("after update = %+v, want term=41 price=3015000 closing=66500", got[0])
	}
}

// TestContractCostFieldsDefaultToZero pins the "cost unknown" contract on
// existing rows: a contract added with no cost data (the pre-backfill state
// for every contract migrated from the old sheet) reads back with all three
// new fields at their zero value.
func TestContractCostFieldsDefaultToZero(t *testing.T) {
	s := openTestStore(t)

	id, err := s.AddContract(context.Background(), Contract{Name: "A", AnnualPoints: 120, UseYearMonth: time.April})
	if err != nil {
		t.Fatalf("AddContract: %v", err)
	}
	got, err := s.ListContracts(context.Background())
	if err != nil {
		t.Fatalf("ListContracts: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ListContracts len = %d, want 1", len(got))
	}
	if got[0].ID != id || got[0].TermYears != 0 || got[0].PurchasePrice != 0 || got[0].ClosingCosts != 0 {
		t.Errorf("ListContracts[0] = %+v, want term=0 price=0 closing=0", got[0])
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	dsn := OpenTestDSN(t)
	s1, err := Open(dsn)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if _, err := s1.AddContract(context.Background(), Contract{Name: "A", AnnualPoints: 120, UseYearMonth: time.April}); err != nil {
		t.Fatalf("AddContract: %v", err)
	}
	s1.Close()

	// Reopening the same schema must not wipe data or fail on existing tables.
	s2, err := Open(dsn)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer s2.Close()
	got, _ := s2.ListContracts(context.Background())
	if len(got) != 1 {
		t.Fatalf("after reopen len = %d, want 1", len(got))
	}
}

// TestContextCancelledBeforeQuery proves the threaded context.Context is
// real, not just a signature change that drops ctx on the floor: a Store
// method called with an already-cancelled context must fail rather than
// reach the database, surfacing an error that errors.Is(err,
// context.Canceled). Verified by mutation: reverting AddContract to pass
// context.Background() to dbgen makes this test fail with err = <nil>,
// because the insert then succeeds.
//
// This deliberately cancels BEFORE the call rather than mid-query. What is
// at risk in this package is a method that accepts a ctx and then quietly
// hands context.Background() to dbgen, which the pre-cancelled form catches
// exactly. Aborting a genuinely in-flight query is a property of
// database/sql and pgx, not of code written here, and testing it through
// the Store API would need a deliberately slow query and a racing
// goroutine — a flaky test of someone else's guarantee.
func TestContextCancelledBeforeQuery(t *testing.T) {
	s := openTestStore(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := s.AddContract(ctx, Contract{Name: "A", AnnualPoints: 120, UseYearMonth: time.April})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("AddContract with a cancelled context: err = %v, want errors.Is(err, context.Canceled)", err)
	}
}
