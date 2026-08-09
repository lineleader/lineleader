package ledger

import (
	"fmt"
	"strconv"
	"strings"
)

// Micros is millionths of a dollar (1e-6), used for per-point rates: contract
// $/pt/yr and dues $/pt. Cent precision is not enough here — $8.2235/pt and
// $5.6796117/pt/yr must survive multiplication by a few hundred points
// without the rounding error landing in whole dollars.
type Micros int64

// Cents is hundredths of a dollar (1e-2), used for totals: purchase prices,
// closing costs, and computed stay costs.
//
// Micros and Cents are distinct named types — rather than a single money
// type — so the compiler catches adding a per-point rate to a dollar total.
type Cents int64

// microsPerCent is the conversion factor between the two money types:
// 1 cent = 10,000 micros.
const microsPerCent = 10_000

// divRound divides n by d and rounds the result half away from zero. This is
// the one rounding rule used everywhere money is divided in this package.
func divRound(n, d int64) int64 {
	neg := (n < 0) != (d < 0)
	if n < 0 {
		n = -n
	}
	if d < 0 {
		d = -d
	}
	q, r := n/d, n%d
	if 2*r >= d {
		q++
	}
	if neg {
		return -q
	}
	return q
}

// parseDecimal parses a decimal string into an integer count of 10^-scale
// units (e.g. scale 2 for cents, scale 6 for micros). It accepts an optional
// leading "-" and "$", thousands separators ("," between digit groups), and
// an empty string (which parses to 0, matching the atoiOr(_, 0) convention
// used elsewhere on these forms). Fewer fractional digits than scale are
// zero-padded; more than scale is a hard error rather than silent rounding.
func parseDecimal(s string, scale int) (int64, error) {
	orig := s
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}

	neg := false
	if strings.HasPrefix(s, "-") {
		neg = true
		s = s[1:]
	}
	s = strings.TrimPrefix(s, "$")
	s = strings.ReplaceAll(s, ",", "")
	if s == "" {
		return 0, fmt.Errorf("parseDecimal(%q): no digits", orig)
	}

	whole, frac, hasFrac := strings.Cut(s, ".")
	if hasFrac {
		if len(frac) > scale {
			return 0, fmt.Errorf("parseDecimal(%q): too many fractional digits (max %d)", orig, scale)
		}
		frac += strings.Repeat("0", scale-len(frac))
	} else {
		frac = strings.Repeat("0", scale)
	}

	n, err := strconv.ParseInt(whole+frac, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parseDecimal(%q): %w", orig, err)
	}
	if neg {
		n = -n
	}
	return n, nil
}

// ParseMicros parses a decimal dollar string into Micros, e.g. "8.2235" ->
// 8_223_500. See parseDecimal for accepted syntax.
func ParseMicros(s string) (Micros, error) {
	n, err := parseDecimal(s, 6)
	if err != nil {
		return 0, err
	}
	return Micros(n), nil
}

// ParseCents parses a decimal dollar string into Cents, e.g. "588.35" ->
// 58_835. See parseDecimal for accepted syntax.
func ParseCents(s string) (Cents, error) {
	n, err := parseDecimal(s, 2)
	if err != nil {
		return 0, err
	}
	return Cents(n), nil
}

// formatFixed renders n — an integer count of 10^-scale units — as a
// fixed-point decimal string with exactly scale digits after the point.
func formatFixed(n int64, scale int) string {
	neg := n < 0
	if neg {
		n = -n
	}
	pow := int64(1)
	for range scale {
		pow *= 10
	}
	whole, frac := n/pow, n%pow
	s := fmt.Sprintf("%d.%0*d", whole, scale, frac)
	if neg {
		s = "-" + s
	}
	return s
}

// trimTrailingZeros strips trailing fractional zeros (and a bare trailing
// decimal point) from a formatFixed-style string, e.g. "8.223500" ->
// "8.2235", "5.000000" -> "5".
func trimTrailingZeros(s string) string {
	if !strings.Contains(s, ".") {
		return s
	}
	s = strings.TrimRight(s, "0")
	return strings.TrimRight(s, ".")
}

// String renders m at minimal precision (trailing fractional zeros trimmed),
// e.g. Micros(8_223_500).String() == "8.2235". It round-trips: ParseMicros
// on the result always returns m.
func (m Micros) String() string {
	return trimTrailingZeros(formatFixed(int64(m), 6))
}

// String renders c as a fixed two-decimal dollar amount, e.g.
// Cents(-123_456).String() == "-1234.56". It round-trips through ParseCents.
func (c Cents) String() string {
	return formatFixed(int64(c), 2)
}

// groupThousands inserts "," separators into the whole-number digit string s
// every three digits from the right, e.g. "1234567" -> "1,234,567".
func groupThousands(s string) string {
	n := len(s)
	if n <= 3 {
		return s
	}
	first := n % 3
	if first == 0 {
		first = 3
	}
	var b strings.Builder
	b.WriteString(s[:first])
	for i := first; i < n; i += 3 {
		b.WriteByte(',')
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

// FormatUSD renders c as a comma-grouped currency string, e.g.
// FormatUSD(123_456) == "$1,234.56", FormatUSD(-123_456) == "-$1,234.56".
func FormatUSD(c Cents) string {
	neg := c < 0
	if neg {
		c = -c
	}
	whole, frac, _ := strings.Cut(formatFixed(int64(c), 2), ".")
	out := "$" + groupThousands(whole) + "." + frac
	if neg {
		out = "-" + out
	}
	return out
}

// FormatRate renders m — a per-point rate — rounded to four decimal places
// (the hundredth-of-a-cent, i.e. 100-micros, precision the owner's sheet
// displays), e.g. FormatRate(5_679_612) == "$5.6796".
func FormatRate(m Micros) string {
	rounded := divRound(int64(m), 100)
	return "$" + formatFixed(rounded, 4)
}

// PointCost prices points points at the given per-point-per-year rate and
// dues, both Micros, rounding the micros total to the nearest cent (half
// away from zero).
func PointCost(points int, rate, dues Micros) Cents {
	total := int64(points) * (int64(rate) + int64(dues))
	return Cents(divRound(total, microsPerCent))
}
