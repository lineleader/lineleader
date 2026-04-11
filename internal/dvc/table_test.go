package dvc

import (
	"strings"
	"testing"
	"time"
)

func TestPrintTable(t *testing.T) {
	checkIn, _ := time.Parse("2006-01-02", "2026-03-15")
	checkOut, _ := time.Parse("2006-01-02", "2026-03-17")

	results := []StayResult{
		{
			Resort:   "Grand Floridian Villas",
			RoomType: "RESORT STUDIO",
			View:     "R",
			CheckIn:  checkIn,
			CheckOut: checkOut,
			Nights:   2,
			Points:   36,
		},
	}

	var sb strings.Builder
	PrintTable(&sb, results, SearchParams{
		WindowStart: checkIn,
		WindowEnd:   checkOut,
		Budget:      100,
	})

	out := sb.String()
	if !strings.Contains(out, "Grand Floridian Villas") {
		t.Errorf("output missing resort name:\n%s", out)
	}
	if !strings.Contains(out, "RESORT STUDIO") {
		t.Errorf("output missing room type:\n%s", out)
	}
	if !strings.Contains(out, "36") {
		t.Errorf("output missing points:\n%s", out)
	}
	if !strings.Contains(out, "1 result") {
		t.Errorf("output missing result count:\n%s", out)
	}
}
