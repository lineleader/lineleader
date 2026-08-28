package dvc

import (
	"reflect"
	"slices"
	"testing"
	"time"
)

func TestEffectiveFilters_InheritIgnoresTripFilters(t *testing.T) {
	global := Config{
		ExcludeResorts:   []string{"VERO", "HH"},
		ExcludeRoomTypes: []string{"THREE-BEDROOM GRAND VILLA"},
	}
	trip := FilterSet{
		ExcludeResorts:   []string{"AKV"},
		ExcludeRoomTypes: []string{"RESORT STUDIO"},
	}

	got := EffectiveFilters(global, FilterModeInherit, trip)

	want := global.AsFilterSet()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("inherit: got %+v, want global %+v", got, want)
	}
}

func TestEffectiveFilters_OverrideReturnsTripFilters(t *testing.T) {
	global := Config{
		ExcludeResorts:   []string{"VERO", "HH"},
		ExcludeRoomTypes: []string{"THREE-BEDROOM GRAND VILLA"},
	}
	trip := FilterSet{
		ExcludeResorts:   []string{"AKV"},
		ExcludeRoomTypes: []string{"RESORT STUDIO"},
	}

	got := EffectiveFilters(global, FilterModeOverride, trip)

	if !reflect.DeepEqual(got, trip) {
		t.Errorf("override: got %+v, want trip %+v", got, trip)
	}
}

func TestEffectiveFilters_OverrideWithEmptyIgnoresGlobal(t *testing.T) {
	global := Config{
		ExcludeResorts:   []string{"VERO", "HH"},
		ExcludeRoomTypes: []string{"THREE-BEDROOM GRAND VILLA"},
	}

	got := EffectiveFilters(global, FilterModeOverride, FilterSet{})

	if len(got.ExcludeResorts) != 0 || len(got.ExcludeRoomTypes) != 0 {
		t.Errorf("override with empty set should yield empty exclusions, got %+v", got)
	}
}

func TestStayEquals_SameFields(t *testing.T) {
	checkIn, _ := time.Parse("2006-01-02", "2026-01-04")
	checkOut, _ := time.Parse("2006-01-02", "2026-01-08")
	a := StayResult{Resort: "VGF", RoomType: "STUDIO", View: "R", CheckIn: checkIn, CheckOut: checkOut}
	b := a
	if !stayEquals(a, b) {
		t.Error("stayEquals returned false for identical stays")
	}
}

func TestStayEquals_DifferentFields(t *testing.T) {
	checkIn, _ := time.Parse("2006-01-02", "2026-01-04")
	checkOut, _ := time.Parse("2006-01-02", "2026-01-08")
	a := StayResult{Resort: "VGF", RoomType: "STUDIO", View: "R", CheckIn: checkIn, CheckOut: checkOut}
	b := a
	b.View = "P"
	if stayEquals(a, b) {
		t.Error("stayEquals returned true for stays with different View")
	}
}

// TestCloneFilterSet_DoesNotAlias proves the reason CloneFilterSet exists:
// mutating the clone's slices must not affect the original's. Before this
// helper existed as a standalone function, an aliasing bug here made a
// per-trip override edit leak back into the global filter set (see the old
// Planner's TestToggleTripResort_SeedsFromGlobalWithoutAliasing).
func TestCloneFilterSet_DoesNotAlias(t *testing.T) {
	orig := FilterSet{
		ExcludeResorts:   []string{"VERO"},
		ExcludeRoomTypes: []string{"STUDIO"},
	}

	clone := CloneFilterSet(orig)
	clone.ExcludeResorts[0] = "MUTATED"
	clone.ExcludeRoomTypes = append(clone.ExcludeRoomTypes, "VILLA")

	if orig.ExcludeResorts[0] != "VERO" {
		t.Errorf("mutating clone's ExcludeResorts affected original: %v", orig.ExcludeResorts)
	}
	if len(orig.ExcludeRoomTypes) != 1 {
		t.Errorf("appending to clone's ExcludeRoomTypes affected original: %v", orig.ExcludeRoomTypes)
	}
}

// twoResortCharts returns two distinct resorts (AAA/STUDIO, BBB/VILLA) so
// FilterOptionsFor's de-dup + sort behaviour can be exercised.
func twoResortCharts() []*ResortChart {
	mk := func(code, room string) *ResortChart {
		return &ResortChart{
			ResortName: code + " Resort",
			ResortCode: code,
			Year:       2026,
			Columns:    []Column{{RoomType: room, View: "R", Sleeps: 4}},
			Seasons: []Season{{
				Periods: []DateRange{{Start: "2026-01-01", End: "2026-01-31"}},
				SunThu:  []int{10},
				FriSat:  []int{14},
			}},
		}
	}
	return []*ResortChart{mk("AAA", "STUDIO"), mk("BBB", "VILLA")}
}

func TestFilterOptions_GlobalSortedDedupedAndEnabled(t *testing.T) {
	charts := twoResortCharts()
	cfg := Config{
		ExcludeResorts:   []string{"AAA"},
		ExcludeRoomTypes: []string{"STUDIO"},
	}

	v := FilterOptionsFor(charts, cfg.AsFilterSet())

	gotCodes := make([]string, len(v.Resorts))
	for i, r := range v.Resorts {
		gotCodes[i] = r.Code
	}
	wantCodes := []string{"AAA", "BBB"} // sorted, de-duped
	if !slices.Equal(gotCodes, wantCodes) {
		t.Errorf("resort codes = %v, want %v", gotCodes, wantCodes)
	}

	gotRooms := make([]string, len(v.RoomTypes))
	for i, r := range v.RoomTypes {
		gotRooms[i] = r.Name
	}
	wantRooms := []string{"STUDIO", "VILLA"} // sorted, de-duped
	if !slices.Equal(gotRooms, wantRooms) {
		t.Errorf("room types = %v, want %v", gotRooms, wantRooms)
	}

	// Enabled reflects the given exclusions.
	for _, r := range v.Resorts {
		want := r.Code != "AAA"
		if r.Enabled != want {
			t.Errorf("resort %s Enabled = %v, want %v", r.Code, r.Enabled, want)
		}
	}
	for _, r := range v.RoomTypes {
		want := r.Name != "STUDIO"
		if r.Enabled != want {
			t.Errorf("room %s Enabled = %v, want %v", r.Name, r.Enabled, want)
		}
	}
}
