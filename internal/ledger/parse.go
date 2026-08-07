package ledger

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// DateLayout is the ledger's canonical date format, shared by storage and every
// user-facing surface (CLI flags, HTML forms, the JSON API) that reads or writes
// a ledger date as a string.
const DateLayout = "2006-01-02"

// ParseMonth accepts a month number (1-12) or an English month name/abbreviation
// (case-insensitive), as used by a contract's use-year-start-month.
func ParseMonth(s string) (time.Month, error) {
	s = strings.TrimSpace(s)
	for m := time.January; m <= time.December; m++ {
		if strings.EqualFold(s, m.String()) || strings.EqualFold(s, m.String()[:3]) {
			return m, nil
		}
	}
	if n, err := strconv.Atoi(s); err == nil && n >= 1 && n <= 12 {
		return time.Month(n), nil
	}
	return 0, fmt.Errorf("invalid month %q (use Apr, April, or 4)", s)
}
