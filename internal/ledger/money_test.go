package ledger

import "testing"

func TestDivRound(t *testing.T) {
	cases := []struct {
		n, d int64
		want int64
	}{
		{5, 2, 3},   // 2.5 rounds up (half away from zero)
		{-5, 2, -3}, // symmetric for negatives
		{4, 2, 2},
		{7, 2, 4}, // 3.5 -> 4
		{1, 3, 0},
		{2, 3, 1},
		{0, 5, 0},
		{29_988_350_000, 5_280, 5_679_612}, // 2019 contract $/pt/yr, in micros
		{30_815_000_000, 6_150, 5_010_569}, // 2022 contract $/pt/yr, in micros
		{1_433_138_790, 270, 5_307_921},    // blended rate, in micros
	}
	for _, c := range cases {
		if got := divRound(c.n, c.d); got != c.want {
			t.Errorf("divRound(%d, %d) = %d, want %d", c.n, c.d, got, c.want)
		}
	}
}

func TestParseDecimal(t *testing.T) {
	cases := []struct {
		in      string
		scale   int
		want    int64
		wantErr bool
	}{
		{"8.2235", 6, 8_223_500, false},
		{"588.35", 2, 58_835, false},
		{"$588.35", 2, 58_835, false},
		{"1,234.56", 2, 123_456, false},
		{"$1,234.56", 2, 123_456, false},
		{"", 2, 0, false},   // blank -> 0, matching atoiOr(_, 0)
		{"5", 2, 500, false}, // no fractional part at all
		{"5.6", 2, 560, false},
		{"5.678", 2, 0, true}, // too many fractional digits for scale 2
		{"abc", 2, 0, true},
		{"-5.25", 2, -525, false},
	}
	for _, c := range cases {
		got, err := parseDecimal(c.in, c.scale)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseDecimal(%q, %d) = %d, nil; want error", c.in, c.scale, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseDecimal(%q, %d) unexpected error: %v", c.in, c.scale, err)
			continue
		}
		if got != c.want {
			t.Errorf("parseDecimal(%q, %d) = %d, want %d", c.in, c.scale, got, c.want)
		}
	}
}

func TestParseMicros(t *testing.T) {
	cases := []struct {
		in      string
		want    Micros
		wantErr bool
	}{
		{"8.2235", 8_223_500, false},
		{"5.6796", 5_679_600, false},
		{"", 0, false},
		{"5.67961234567", 0, true}, // more than 6 fractional digits
	}
	for _, c := range cases {
		got, err := ParseMicros(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParseMicros(%q) = %v, nil; want error", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseMicros(%q) unexpected error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseMicros(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestParseCents(t *testing.T) {
	cases := []struct {
		in      string
		want    Cents
		wantErr bool
	}{
		{"588.35", 58_835, false},
		{"$29,400", 2_940_000, false},
		{"665", 66_500, false},
		{"", 0, false},
		{"588.355", 0, true}, // more than 2 fractional digits
	}
	for _, c := range cases {
		got, err := ParseCents(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParseCents(%q) = %v, nil; want error", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseCents(%q) unexpected error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseCents(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestMicrosString(t *testing.T) {
	cases := []struct {
		in   Micros
		want string
	}{
		{8_223_500, "8.2235"},
		{5_679_612, "5.679612"},
		{0, "0"},
		{-5_500_000, "-5.5"},
	}
	for _, c := range cases {
		got := c.in.String()
		if got != c.want {
			t.Errorf("Micros(%d).String() = %q, want %q", c.in, got, c.want)
		}
		reparsed, err := ParseMicros(got)
		if err != nil {
			t.Fatalf("ParseMicros(%q) round-trip error: %v", got, err)
		}
		if reparsed != c.in {
			t.Errorf("round-trip: Micros(%d).String() = %q, ParseMicros(...) = %d", c.in, got, reparsed)
		}
	}
}

func TestCentsString(t *testing.T) {
	cases := []struct {
		in   Cents
		want string
	}{
		{123_456, "1234.56"},
		{-123_456, "-1234.56"},
		{0, "0.00"},
	}
	for _, c := range cases {
		if got := c.in.String(); got != c.want {
			t.Errorf("Cents(%d).String() = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFormatUSD(t *testing.T) {
	cases := []struct {
		in   Cents
		want string
	}{
		{123_456, "$1,234.56"},
		{-123_456, "-$1,234.56"},
		{0, "$0.00"},
		{99, "$0.99"},
		{100_000_000, "$1,000,000.00"},
	}
	for _, c := range cases {
		if got := FormatUSD(c.in); got != c.want {
			t.Errorf("FormatUSD(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFormatRate(t *testing.T) {
	cases := []struct {
		in   Micros
		want string
	}{
		{5_679_612, "$5.6796"},
		{5_010_569, "$5.0106"},
		{5_307_921, "$5.3079"},
		{8_223_500, "$8.2235"},
	}
	for _, c := range cases {
		if got := FormatRate(c.in); got != c.want {
			t.Errorf("FormatRate(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestPointCost(t *testing.T) {
	cases := []struct {
		points     int
		rate, dues Micros
		want       Cents
	}{
		{100, 5_679_612, 7_929_800, 136_094}, // 100 pts / UY2025 / 2019 contract
		{240, 5_307_921, 6_811_800, 290_873}, // 240 pts / UY2021 / blended
	}
	for _, c := range cases {
		if got := PointCost(c.points, c.rate, c.dues); got != c.want {
			t.Errorf("PointCost(%d, %d, %d) = %d, want %d", c.points, c.rate, c.dues, got, c.want)
		}
	}
}
