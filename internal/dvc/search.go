package dvc

import (
	"fmt"
	"sort"
	"time"
)

// isWeekend reports whether d is a Friday or Saturday (DVC FRI-SAT rate applies).
func isWeekend(d time.Time) bool {
	wd := d.Weekday()
	return wd == time.Friday || wd == time.Saturday
}

// PointsForNight returns the per-night points for the given column on the given date.
// Returns an error if no season covers that date.
func PointsForNight(chart *ResortChart, date time.Time, colIdx int) (int, error) {
	date = date.UTC().Truncate(24 * time.Hour)
	for _, s := range chart.Seasons {
		for _, period := range s.Periods {
			ok, err := period.Contains(date)
			if err != nil {
				return 0, err
			}
			if ok {
				if isWeekend(date) {
					return s.FriSat[colIdx], nil
				}
				return s.SunThu[colIdx], nil
			}
		}
	}
	return 0, fmt.Errorf("no season covers date %s in resort %s", date.Format("2006-01-02"), chart.ResortCode)
}

// Search enumerates all (checkIn, checkOut) pairs within the window across all
// charts and columns, returning stays whose total points ≤ budget.
// Results are sorted ascending by Points, then by CheckIn date.
func Search(charts []*ResortChart, params SearchParams) []StayResult {
	minNights := params.MinNights
	if minNights < 1 {
		minNights = 1
	}

	var results []StayResult

	for _, chart := range charts {
		for colIdx, col := range chart.Columns {
			checkIn := params.WindowStart.UTC().Truncate(24 * time.Hour)
			windowEnd := params.WindowEnd.UTC().Truncate(24 * time.Hour)

			for checkIn.Before(windowEnd) {
				total := 0
				checkOut := checkIn.Add(24 * time.Hour)

				for !checkOut.After(windowEnd) {
					// The night being charged is the night starting on checkOut-1day.
					night := checkOut.Add(-24 * time.Hour)
					pts, err := PointsForNight(chart, night, colIdx)
					if err != nil {
						break // date gap — stop extending this stay
					}
					total += pts
					if total > params.Budget {
						break // over budget — longer stays won't help
					}
					nights := int(checkOut.Sub(checkIn).Hours() / 24)
					if nights >= minNights {
						results = append(results, StayResult{
							Resort:   chart.ResortName,
							RoomType: col.RoomType,
							View:     col.View,
							CheckIn:  checkIn,
							CheckOut: checkOut,
							Nights:   nights,
							Points:   total,
						})
					}
					checkOut = checkOut.Add(24 * time.Hour)
				}
				checkIn = checkIn.Add(24 * time.Hour)
			}
		}
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].Points != results[j].Points {
			return results[i].Points < results[j].Points
		}
		return results[i].CheckIn.Before(results[j].CheckIn)
	})

	return results
}
