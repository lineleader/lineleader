package ledger

import (
	"testing"
	"time"
)

func TestParseMonth(t *testing.T) {
	cases := []struct {
		in      string
		want    time.Month
		wantErr bool
	}{
		{"April", time.April, false},
		{"apr", time.April, false},
		{"APR", time.April, false},
		{"december", time.December, false},
		{"4", time.April, false},
		{"12", time.December, false},
		{"1", time.January, false},
		{"0", 0, true},
		{"13", 0, true},
		{"notamonth", 0, true},
		{"", 0, true},
	}
	for _, c := range cases {
		got, err := ParseMonth(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParseMonth(%q) = %v, nil; want error", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseMonth(%q) unexpected error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseMonth(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
