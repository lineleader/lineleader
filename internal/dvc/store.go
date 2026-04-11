package dvc

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// SaveChart writes chart to dataDir/<year>_<ResortCode>.json.
func SaveChart(dataDir string, chart *ResortChart) error {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return fmt.Errorf("creating data dir: %w", err)
	}
	filename := fmt.Sprintf("%d_%s.json", chart.Year, chart.ResortCode)
	path := filepath.Join(dataDir, filename)

	data, err := json.MarshalIndent(chart, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling chart: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}

// LoadAll reads all *.json files from dataDir and returns the parsed charts.
func LoadAll(dataDir string) ([]*ResortChart, error) {
	entries, err := filepath.Glob(filepath.Join(dataDir, "*.json"))
	if err != nil {
		return nil, fmt.Errorf("globbing data dir: %w", err)
	}

	var charts []*ResortChart
	for _, path := range entries {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", path, err)
		}
		var chart ResortChart
		if err := json.Unmarshal(data, &chart); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", path, err)
		}
		charts = append(charts, &chart)
	}
	return charts, nil
}
