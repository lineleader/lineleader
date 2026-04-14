package dvc

import (
	"testing"
	"time"
)

func TestIsWeekend(t *testing.T) {
	cases := []struct {
		date string
		want bool
	}{
		{"2026-01-02", true},  // Friday → FRI-SAT rate
		{"2026-01-03", true},  // Saturday → FRI-SAT rate
		{"2026-01-04", false}, // Sunday → SUN-THU rate
		{"2026-01-05", false}, // Monday → SUN-THU rate
		{"2026-01-09", true},  // Friday → FRI-SAT rate
	}
	for _, c := range cases {
		d, _ := time.Parse("2006-01-02", c.date)
		if got := isWeekend(d); got != c.want {
			t.Errorf("isWeekend(%s [%s]) = %v, want %v", c.date, d.Weekday(), got, c.want)
		}
	}
}

// minimalChart returns a chart with one season and two columns for testing.
func minimalChart() *ResortChart {
	return &ResortChart{
		ResortName: "Test Resort",
		ResortCode: "TST",
		Year:       2026,
		Columns: []Column{
			{RoomType: "STUDIO", View: "R", Sleeps: 4},
			{RoomType: "STUDIO", View: "P", Sleeps: 4},
		},
		Seasons: []Season{
			{
				Periods: []DateRange{
					{Start: "2026-01-01", End: "2026-01-31"},
				},
				SunThu: []int{10, 15},
				FriSat: []int{14, 20},
			},
		},
	}
}

func TestPointsForNight(t *testing.T) {
	chart := minimalChart()

	// Jan 5 2026 = Monday → SunThu rate
	mon, _ := time.Parse("2006-01-02", "2026-01-05")
	pts, err := PointsForNight(chart, mon, 0)
	if err != nil || pts != 10 {
		t.Errorf("Monday SunThu col0: got %d, %v; want 10", pts, err)
	}

	// Jan 9 2026 = Friday → FriSat rate
	fri, _ := time.Parse("2006-01-02", "2026-01-09")
	pts, err = PointsForNight(chart, fri, 1)
	if err != nil || pts != 20 {
		t.Errorf("Friday FriSat col1: got %d, %v; want 20", pts, err)
	}

	// Date outside season → error
	feb, _ := time.Parse("2006-01-02", "2026-02-01")
	_, err = PointsForNight(chart, feb, 0)
	if err == nil {
		t.Error("expected error for date outside season, got nil")
	}
}

func TestSearch_BasicBudget(t *testing.T) {
	chart := minimalChart()
	// Column 0: 10 pts/weeknight, 14 pts/weekend night
	// Column 1: 15 pts/weeknight, 20 pts/weekend night
	// Search Jan 4-8 (Sun-Thu, 4 weeknights), budget 45
	// Col0: 4×10 = 40 ≤ 45 ✓  Col1: 4×15 = 60 > 45 ✗
	start, _ := time.Parse("2006-01-02", "2026-01-04")
	end, _ := time.Parse("2006-01-02", "2026-01-08")

	results := Search([]*ResortChart{chart}, SearchParams{
		WindowStart: start,
		WindowEnd:   end,
		Budget:      45,
		MinNights:   4,
	})

	// Should find exactly the 4-night stay for col0 (and shorter stays for both cols)
	found4NightCol0 := false
	for _, r := range results {
		if r.Nights == 4 && r.RoomType == "STUDIO" && r.View == "R" && r.Points == 40 {
			found4NightCol0 = true
		}
		if r.Nights == 4 && r.View == "P" {
			t.Errorf("col1 4-night stay should exceed budget: %+v", r)
		}
	}
	if !found4NightCol0 {
		t.Errorf("expected 4-night col0 stay with 40 pts; results: %+v", results)
	}
}

func TestSearch_ExcludeResort(t *testing.T) {
	chart := minimalChart() // ResortCode = "TST"
	start, _ := time.Parse("2006-01-02", "2026-01-04")
	end, _ := time.Parse("2006-01-02", "2026-01-08")

	results := Search([]*ResortChart{chart}, SearchParams{
		WindowStart:    start,
		WindowEnd:      end,
		Budget:         200,
		MinNights:      1,
		ExcludeResorts: []string{"TST"},
	})
	if len(results) != 0 {
		t.Errorf("expected 0 results when resort is excluded, got %d", len(results))
	}
}

func TestSearch_ExcludeRoomType(t *testing.T) {
	chart := minimalChart() // columns: STUDIO/R and STUDIO/P
	start, _ := time.Parse("2006-01-02", "2026-01-04")
	end, _ := time.Parse("2006-01-02", "2026-01-08")

	results := Search([]*ResortChart{chart}, SearchParams{
		WindowStart:      start,
		WindowEnd:        end,
		Budget:           200,
		MinNights:        1,
		ExcludeRoomTypes: []string{"STUDIO"},
	})
	if len(results) != 0 {
		t.Errorf("expected 0 results when room type is excluded, got %d", len(results))
	}
}

func TestSearch_SortedByPoints(t *testing.T) {
	chart := minimalChart()
	start, _ := time.Parse("2006-01-02", "2026-01-04")
	end, _ := time.Parse("2006-01-02", "2026-01-08")

	results := Search([]*ResortChart{chart}, SearchParams{
		WindowStart: start,
		WindowEnd:   end,
		Budget:      200,
		MinNights:   1,
	})

	for i := 1; i < len(results); i++ {
		if results[i].Points < results[i-1].Points {
			t.Errorf("results not sorted: [%d].Points=%d < [%d].Points=%d",
				i, results[i].Points, i-1, results[i-1].Points)
		}
	}
}
