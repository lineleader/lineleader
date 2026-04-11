package dvc

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveAndLoadChart(t *testing.T) {
	dir := t.TempDir()

	chart := &ResortChart{
		ResortName: "Test Resort",
		ResortCode: "TST",
		Year:       2026,
		Columns: []Column{
			{RoomType: "STUDIO", View: "R", Sleeps: 4},
		},
		Seasons: []Season{
			{
				Periods: []DateRange{{Start: "2026-01-01", End: "2026-01-31"}},
				SunThu:  []int{10},
				FriSat:  []int{15},
			},
		},
	}

	if err := SaveChart(dir, chart); err != nil {
		t.Fatalf("SaveChart: %v", err)
	}

	want := filepath.Join(dir, "2026_TST.json")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("expected file %s: %v", want, err)
	}

	charts, err := LoadAll(dir)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(charts) != 1 {
		t.Fatalf("LoadAll returned %d charts, want 1", len(charts))
	}

	got := charts[0]
	if got.ResortCode != "TST" || got.Year != 2026 {
		t.Errorf("loaded chart = %+v", got)
	}
	if len(got.Columns) != 1 || got.Columns[0].View != "R" {
		t.Errorf("columns = %+v", got.Columns)
	}
	if got.Seasons[0].SunThu[0] != 10 {
		t.Errorf("SunThu[0] = %d, want 10", got.Seasons[0].SunThu[0])
	}
}
