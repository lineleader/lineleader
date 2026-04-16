package dvc

import (
	"os"
	"path/filepath"
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
