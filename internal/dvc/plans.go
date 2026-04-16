package dvc

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// TripSpec is the serialisable form of one trip's input fields.
type TripSpec struct {
	From      string `json:"from"`
	To        string `json:"to"`
	MinNights string `json:"min_nights"`
}

// Plan is a named, saveable set of trips plus the global budget and filter
// state that was active when the plan was saved.
type Plan struct {
	Name             string     `json:"name"`
	Budget           string     `json:"budget"`
	Trips            []TripSpec `json:"trips"`
	ExcludeResorts   []string   `json:"exclude_resorts,omitempty"`
	ExcludeRoomTypes []string   `json:"exclude_room_types,omitempty"`
}

// plansFile is the top-level JSON envelope for plans.json.
type plansFile struct {
	Plans []Plan `json:"plans"`
}

// DefaultPlansPath returns the platform-appropriate path for the plans file.
func DefaultPlansPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = os.Getenv("HOME")
	}
	return filepath.Join(dir, "lineleader", "plans.json")
}

// LoadPlans reads the plans from path.
// If the file does not exist, nil and nil are returned.
func LoadPlans(path string) ([]Plan, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var pf plansFile
	if err := json.Unmarshal(data, &pf); err != nil {
		return nil, err
	}
	return pf.Plans, nil
}

// SavePlans writes plans to path, creating parent directories as needed.
// It uses an atomic write (temp file + rename) to avoid corruption.
func SavePlans(path string, plans []Plan) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}
	data, err := json.MarshalIndent(plansFile{Plans: plans}, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
