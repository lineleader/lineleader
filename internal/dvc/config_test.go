package dvc

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadConfig_MissingFile(t *testing.T) {
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "nonexistent.json"))
	if err != nil {
		t.Fatalf("expected nil error for missing file, got: %v", err)
	}
	if len(cfg.ExcludeResorts) != 0 || len(cfg.ExcludeRoomTypes) != 0 {
		t.Errorf("expected empty config for missing file, got: %+v", cfg)
	}
}

func TestLoadConfig_ValidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	content := `{"exclude_resorts":["VERO","HH"],"exclude_room_types":["THREE-BEDROOM GRAND VILLA"]}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.ExcludeResorts) != 2 || cfg.ExcludeResorts[0] != "VERO" || cfg.ExcludeResorts[1] != "HH" {
		t.Errorf("ExcludeResorts = %v, want [VERO HH]", cfg.ExcludeResorts)
	}
	if len(cfg.ExcludeRoomTypes) != 1 || cfg.ExcludeRoomTypes[0] != "THREE-BEDROOM GRAND VILLA" {
		t.Errorf("ExcludeRoomTypes = %v, want [THREE-BEDROOM GRAND VILLA]", cfg.ExcludeRoomTypes)
	}
}

func TestSaveConfig_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.json")
	want := Config{
		ExcludeResorts:   []string{"VERO", "HH"},
		ExcludeRoomTypes: []string{"THREE-BEDROOM GRAND VILLA"},
	}
	if err := SaveConfig(path, want); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	got, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round-trip mismatch:\n got  %+v\n want %+v", got, want)
	}
}
