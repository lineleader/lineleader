package dvc

import (
	"testing"
	"time"
)

func TestDateRangeContains(t *testing.T) {
	dr := DateRange{Start: "2026-01-01", End: "2026-01-31"}

	cases := []struct {
		date string
		want bool
	}{
		{"2026-01-01", true},  // start boundary
		{"2026-01-15", true},  // middle
		{"2026-01-31", true},  // end boundary
		{"2025-12-31", false}, // before
		{"2026-02-01", false}, // after
	}

	for _, c := range cases {
		d, _ := time.Parse("2006-01-02", c.date)
		got, err := dr.Contains(d)
		if err != nil {
			t.Fatalf("Contains(%s) error: %v", c.date, err)
		}
		if got != c.want {
			t.Errorf("Contains(%s) = %v, want %v", c.date, got, c.want)
		}
	}
}
