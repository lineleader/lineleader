package dvc

import (
	"reflect"
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

func TestConfigAsFilterSet(t *testing.T) {
	cfg := Config{
		ExcludeResorts:   []string{"VGF"},
		ExcludeRoomTypes: []string{"STUDIO"},
	}

	got := cfg.AsFilterSet()

	if !reflect.DeepEqual(got.ExcludeResorts, []string{"VGF"}) {
		t.Errorf("ExcludeResorts = %v, want %v", got.ExcludeResorts, []string{"VGF"})
	}
	if !reflect.DeepEqual(got.ExcludeRoomTypes, []string{"STUDIO"}) {
		t.Errorf("ExcludeRoomTypes = %v, want %v", got.ExcludeRoomTypes, []string{"STUDIO"})
	}
}

func TestConfigAsFilterSetEmpty(t *testing.T) {
	got := Config{}.AsFilterSet()

	if got.ExcludeResorts != nil {
		t.Errorf("ExcludeResorts = %v, want nil", got.ExcludeResorts)
	}
	if got.ExcludeRoomTypes != nil {
		t.Errorf("ExcludeRoomTypes = %v, want nil", got.ExcludeRoomTypes)
	}
}

func TestFilterModeValues(t *testing.T) {
	if FilterModeInherit != "" {
		t.Errorf("FilterModeInherit = %q, want %q", FilterModeInherit, "")
	}
	if FilterModeOverride != "override" {
		t.Errorf("FilterModeOverride = %q, want %q", FilterModeOverride, "override")
	}
}
