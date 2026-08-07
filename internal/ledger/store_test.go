package ledger

import (
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
	id, err := s.AddContract(c)
	if err != nil {
		t.Fatalf("AddContract: %v", err)
	}
	if id == 0 {
		t.Fatal("AddContract returned id 0")
	}

	got, err := s.ListContracts()
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
	if err := s.UpdateContract(c); err != nil {
		t.Fatalf("UpdateContract: %v", err)
	}
	got, _ = s.ListContracts()
	if got[0].AnnualPoints != 160 || got[0].Number != "7654321.000" {
		t.Errorf("after update = %+v, want points=160 number=7654321.000", got[0])
	}

	if err := s.DeleteContract(id); err != nil {
		t.Fatalf("DeleteContract: %v", err)
	}
	got, _ = s.ListContracts()
	if len(got) != 0 {
		t.Fatalf("after delete len = %d, want 0", len(got))
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	dsn := OpenTestDSN(t)
	s1, err := Open(dsn)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if _, err := s1.AddContract(Contract{Name: "A", AnnualPoints: 120, UseYearMonth: time.April}); err != nil {
		t.Fatalf("AddContract: %v", err)
	}
	s1.Close()

	// Reopening the same schema must not wipe data or fail on existing tables.
	s2, err := Open(dsn)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer s2.Close()
	got, _ := s2.ListContracts()
	if len(got) != 1 {
		t.Fatalf("after reopen len = %d, want 1", len(got))
	}
}
