package dvc

import (
	"encoding/json"
	"errors"
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
