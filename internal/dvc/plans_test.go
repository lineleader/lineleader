package dvc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoadPlans_MissingFile(t *testing.T) {
	plans, err := LoadPlans(filepath.Join(t.TempDir(), "nonexistent.json"))
	if err != nil {
		t.Fatalf("expected nil error for missing file, got: %v", err)
	}
	if plans != nil {
		t.Errorf("expected nil plans for missing file, got: %+v", plans)
	}
}

func TestSavePlans_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "plans.json")

	plans := []Plan{
		{
			Name:             "spring-break",
			Budget:           "250",
			Trips:            []TripSpec{{From: "2026-03-15", To: "2026-03-22", MinNights: "3"}},
			ExcludeResorts:   []string{"VERO"},
			ExcludeRoomTypes: []string{"THREE-BEDROOM GRAND VILLA"},
		},
		{
			Name:   "summer",
			Budget: "300",
			Trips: []TripSpec{
				{From: "2026-07-01", To: "2026-07-07", MinNights: "2"},
				{From: "2026-07-10", To: "2026-07-15", MinNights: "1"},
			},
		},
	}

	if err := SavePlans(path, plans); err != nil {
		t.Fatalf("SavePlans error: %v", err)
	}

	loaded, err := LoadPlans(path)
	if err != nil {
		t.Fatalf("LoadPlans error: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("loaded %d plans, want 2", len(loaded))
	}

	p := loaded[0]
	if p.Name != "spring-break" {
		t.Errorf("Name = %q, want %q", p.Name, "spring-break")
	}
	if p.Budget != "250" {
		t.Errorf("Budget = %q, want %q", p.Budget, "250")
	}
	if len(p.Trips) != 1 {
		t.Fatalf("trips = %d, want 1", len(p.Trips))
	}
	if p.Trips[0].From != "2026-03-15" {
		t.Errorf("Trips[0].From = %q, want %q", p.Trips[0].From, "2026-03-15")
	}
	if len(p.ExcludeResorts) != 1 || p.ExcludeResorts[0] != "VERO" {
		t.Errorf("ExcludeResorts = %v, want [VERO]", p.ExcludeResorts)
	}
	if len(p.ExcludeRoomTypes) != 1 {
		t.Errorf("ExcludeRoomTypes = %v, want 1 entry", p.ExcludeRoomTypes)
	}

	p2 := loaded[1]
	if p2.Name != "summer" {
		t.Errorf("Name = %q, want %q", p2.Name, "summer")
	}
	if len(p2.Trips) != 2 {
		t.Errorf("trips = %d, want 2", len(p2.Trips))
	}
}

func TestSavePlans_CreatesParentDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deeply", "nested", "plans.json")

	if err := SavePlans(path, []Plan{}); err != nil {
		t.Fatalf("SavePlans error: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected file to exist at %s: %v", path, err)
	}
}

func TestTripSpec_MarshalInheritOmitsFilterFields(t *testing.T) {
	spec := TripSpec{From: "2026-03-15", To: "2026-03-22", MinNights: "3"}

	data, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	got := string(data)

	if strings.Contains(got, "filter_mode") {
		t.Errorf("inherit TripSpec JSON should not contain filter_mode: %s", got)
	}
	if strings.Contains(got, "filters") {
		t.Errorf("inherit TripSpec JSON should not contain filters: %s", got)
	}
}

func TestTripSpec_UnmarshalLegacyIsInherit(t *testing.T) {
	legacy := `{"from":"2026-03-15","to":"2026-03-22","min_nights":"2"}`

	var spec TripSpec
	if err := json.Unmarshal([]byte(legacy), &spec); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if spec.FilterMode != FilterModeInherit {
		t.Errorf("FilterMode = %q, want inherit (%q)", spec.FilterMode, FilterModeInherit)
	}
	if spec.Filters != nil {
		t.Errorf("Filters = %+v, want nil", spec.Filters)
	}
}

func TestTripSpec_OverrideRoundTrip(t *testing.T) {
	spec := TripSpec{
		From:       "2026-03-15",
		To:         "2026-03-22",
		MinNights:  "3",
		FilterMode: FilterModeOverride,
		Filters: &FilterSet{
			ExcludeResorts:   []string{"VERO"},
			ExcludeRoomTypes: []string{"THREE-BEDROOM GRAND VILLA"},
		},
	}

	data, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	var got TripSpec
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if !reflect.DeepEqual(got, spec) {
		t.Errorf("round trip mismatch:\n got = %+v\nwant = %+v", got, spec)
	}
}

func TestLoadPlans_LegacyFileReadsAsInherit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plans.json")
	legacy := `{
		"plans": [
			{
				"name": "spring-break",
				"budget": "250",
				"trips": [
					{"from": "2026-03-15", "to": "2026-03-22", "min_nights": "3"},
					{"from": "2026-07-01", "to": "2026-07-07", "min_nights": "2"}
				]
			}
		]
	}`
	if err := os.WriteFile(path, []byte(legacy), 0644); err != nil {
		t.Fatal(err)
	}

	plans, err := LoadPlans(path)
	if err != nil {
		t.Fatalf("LoadPlans error: %v", err)
	}
	if len(plans) != 1 {
		t.Fatalf("loaded %d plans, want 1", len(plans))
	}
	for i, trip := range plans[0].Trips {
		if trip.FilterMode != FilterModeInherit {
			t.Errorf("Trips[%d].FilterMode = %q, want inherit", i, trip.FilterMode)
		}
		if trip.Filters != nil {
			t.Errorf("Trips[%d].Filters = %+v, want nil", i, trip.Filters)
		}
	}
}

func TestLoadPlans_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plans.json")
	if err := os.WriteFile(path, []byte("not json"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadPlans(path)
	if err == nil {
		t.Error("expected error for invalid JSON, got nil")
	}
}
