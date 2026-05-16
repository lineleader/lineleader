package dvc

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Config holds application-level settings loaded from ~/.config/lineleader/config.json.
type Config struct {
	ExcludeResorts   []string `json:"exclude_resorts"`
	ExcludeRoomTypes []string `json:"exclude_room_types"`
}

// DefaultConfigPath returns the platform-appropriate path for the config file.
func DefaultConfigPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = os.Getenv("HOME")
	}
	return filepath.Join(dir, "lineleader", "config.json")
}

// LoadConfig reads the JSON config file at path.
// If the file does not exist, an empty Config and nil error are returned.
func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// SaveConfig writes cfg to path, creating parent directories as needed.
// It uses an atomic write (temp file + rename) to avoid corruption.
func SaveConfig(path string, cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
