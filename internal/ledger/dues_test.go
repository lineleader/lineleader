package ledger

import "testing"

// TestDuesRatesSeeded pins the eight-year dues series from the owner's
// sheet: a freshly opened store (Open runs schema.sql then seed.sql) lists
// them ascending by use year with no database interaction from the test
// itself beyond opening the store.
func TestDuesRatesSeeded(t *testing.T) {
	s := openTestStore(t)

	got, err := s.ListDuesRates()
	if err != nil {
		t.Fatalf("ListDuesRates: %v", err)
	}
	want := []DuesRate{
		{UseYear: 2019, Rate: 6_385_000},
		{UseYear: 2020, Rate: 6_561_600},
		{UseYear: 2021, Rate: 6_811_800},
		{UseYear: 2022, Rate: 7_007_700},
		{UseYear: 2023, Rate: 7_333_200},
		{UseYear: 2024, Rate: 7_574_000},
		{UseYear: 2025, Rate: 7_929_800},
		{UseYear: 2026, Rate: 8_223_500},
	}
	if len(got) != len(want) {
		t.Fatalf("ListDuesRates len = %d, want %d (%+v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ListDuesRates[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestDuesRateUpsertAndDelete(t *testing.T) {
	s := openTestStore(t)

	// Insert a new year.
	if err := s.UpsertDuesRate(DuesRate{UseYear: 2027, Rate: 8_500_000}); err != nil {
		t.Fatalf("UpsertDuesRate (insert): %v", err)
	}
	got, err := s.ListDuesRates()
	if err != nil {
		t.Fatalf("ListDuesRates: %v", err)
	}
	if len(got) != 9 || got[8] != (DuesRate{UseYear: 2027, Rate: 8_500_000}) {
		t.Fatalf("after insert, ListDuesRates = %+v", got)
	}

	// Update an existing year in place.
	if err := s.UpsertDuesRate(DuesRate{UseYear: 2027, Rate: 8_600_000}); err != nil {
		t.Fatalf("UpsertDuesRate (update): %v", err)
	}
	got, _ = s.ListDuesRates()
	if len(got) != 9 || got[8] != (DuesRate{UseYear: 2027, Rate: 8_600_000}) {
		t.Fatalf("after update, ListDuesRates = %+v", got)
	}

	// Delete it back out.
	if err := s.DeleteDuesRate(2027); err != nil {
		t.Fatalf("DeleteDuesRate: %v", err)
	}
	got, _ = s.ListDuesRates()
	if len(got) != 8 {
		t.Fatalf("after delete, ListDuesRates len = %d, want 8 (%+v)", len(got), got)
	}
}

// TestUpsertDuesRateRejectsBadInput covers the store-boundary validation: a
// dues rate must be positive (Rate <= 0 would later reach compoundDues'
// division and panic if it weren't filtered — see cost_test.go's
// TestNewCostBasisIgnoresNonPositiveDuesRates for that belt-and-braces side)
// and UseYear is loosely sanity-checked as a plausible calendar year. Every
// case must return an error and leave the store untouched — no row for that
// year at all, not even a zero-valued one.
func TestUpsertDuesRateRejectsBadInput(t *testing.T) {
	s := OpenTest(t)

	cases := []struct {
		name string
		d    DuesRate
	}{
		{"zero rate", DuesRate{UseYear: 2099, Rate: 0}},
		{"negative rate", DuesRate{UseYear: 2098, Rate: -1}},
		{"implausible year", DuesRate{UseYear: 30000, Rate: 1_000_000}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := s.UpsertDuesRate(c.d); err == nil {
				t.Fatalf("UpsertDuesRate(%+v) = nil error, want error", c.d)
			}
			got, err := s.ListDuesRates()
			if err != nil {
				t.Fatalf("ListDuesRates: %v", err)
			}
			for _, row := range got {
				if row.UseYear == c.d.UseYear {
					t.Fatalf("UpsertDuesRate(%+v) stored a row: %+v", c.d, row)
				}
			}
		})
	}
}

// TestDuesSeedDoesNotResurrectDeletion is the whole-table-guard regression:
// seed.sql only inserts when dues_rates is entirely empty, so an operator
// deleting one seeded year must see it stay gone across a restart (a second
// Open on the same database), not reappear via per-row ON CONFLICT.
func TestDuesSeedDoesNotResurrectDeletion(t *testing.T) {
	dsn := OpenTestDSN(t)
	s1, err := Open(dsn)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if err := s1.DeleteDuesRate(2019); err != nil {
		t.Fatalf("DeleteDuesRate: %v", err)
	}
	got, _ := s1.ListDuesRates()
	if len(got) != 7 {
		t.Fatalf("after delete len = %d, want 7", len(got))
	}
	s1.Close()

	s2, err := Open(dsn)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer s2.Close()
	got, err = s2.ListDuesRates()
	if err != nil {
		t.Fatalf("ListDuesRates after reopen: %v", err)
	}
	if len(got) != 7 {
		t.Fatalf("after reopen len = %d, want 7 (deleted year must not resurrect): %+v", len(got), got)
	}
	for _, d := range got {
		if d.UseYear == 2019 {
			t.Errorf("2019 resurrected after reopen: %+v", got)
		}
	}
}
