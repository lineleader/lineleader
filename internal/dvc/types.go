package dvc

import "time"

// ResortChart holds all point chart data for one resort in one calendar year.
type ResortChart struct {
	ResortName string   `json:"resort_name"`
	ResortCode string   `json:"resort_code"`
	Year       int      `json:"year"`
	Columns    []Column `json:"columns"`
	Seasons    []Season `json:"seasons"`
}

// Column defines one bookable room/view combination in order as they appear in the chart.
type Column struct {
	RoomType string `json:"room_type"` // e.g. "RESORT STUDIO", "ONE-BEDROOM VILLA"
	View     string `json:"view"`      // e.g. "R", "P", "TP"; empty if no view distinction
	Sleeps   int    `json:"sleeps"`
}

// Season maps one or more date ranges to per-column nightly point costs.
// Weekly rates are omitted — they equal 5×SunThu + 2×FriSat.
type Season struct {
	Periods []DateRange `json:"periods"` // 1–3 date ranges sharing this rate tier
	SunThu  []int       `json:"sun_thu"` // per-column points for Sun–Thu nights
	FriSat  []int       `json:"fri_sat"` // per-column points for Fri–Sat nights
}

// DateRange is an inclusive start/end pair stored as YYYY-MM-DD strings.
type DateRange struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

// StartTime parses the Start field into a time.Time (midnight UTC).
func (dr DateRange) StartTime() (time.Time, error) {
	return time.Parse("2006-01-02", dr.Start)
}

// EndTime parses the End field into a time.Time (midnight UTC).
func (dr DateRange) EndTime() (time.Time, error) {
	return time.Parse("2006-01-02", dr.End)
}

// Contains reports whether d falls within this DateRange (inclusive).
// d is compared as a date only (time-of-day is ignored).
func (dr DateRange) Contains(d time.Time) (bool, error) {
	start, err := dr.StartTime()
	if err != nil {
		return false, err
	}
	end, err := dr.EndTime()
	if err != nil {
		return false, err
	}
	d = d.UTC().Truncate(24 * time.Hour)
	return !d.Before(start) && !d.After(end), nil
}

// SearchParams holds user-provided query parameters for a stay search.
type SearchParams struct {
	WindowStart      time.Time
	WindowEnd        time.Time
	Budget           int
	MinNights        int      // 0 or 1 both mean "at least 1 night"
	ExcludeResorts   []string // resort codes to skip entirely
	ExcludeRoomTypes []string // room type strings to skip
}

// StayResult is one matching stay from a search.
type StayResult struct {
	Resort   string
	RoomType string
	View     string
	CheckIn  time.Time
	CheckOut time.Time
	Nights   int
	Points   int
}
