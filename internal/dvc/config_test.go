package dvc

import (
	"os"
	"path/filepath"
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
