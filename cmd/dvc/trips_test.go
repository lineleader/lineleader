package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runTripsT(t *testing.T, server string, args ...string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	err := runTrips(append(append([]string{}, args...), "--server", server), &buf)
	return buf.String(), err
}

func TestRunTripsImport(t *testing.T) {
	t.Setenv("LINELEADER_SERVER", "")
	srv := testServer(t)
	for _, used := range []string{"50", "30"} {
		if _, err := runLedgerT(t, srv, "add", "--date", "2025-03-01", "--desc", "RIV", "--kind", "usage", "--used", used); err != nil {
			t.Fatal(err)
		}
	}
	file := filepath.Join(t.TempDir(), "trips.json")
	body := `[
 {"name":"March","stays":[{"resort":"Riviera","room_type":"Studio","view":"Std","check_in":"2025-03-01","check_out":"2025-03-05","points":50,"entry_ids":[1]}]},
 {"name":"April","stays":[{"resort":"Riviera","room_type":"Studio","view":"Std","check_in":"2025-04-01","check_out":"2025-04-04","points":30,"entry_ids":[2]}]}
]`
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := runTripsT(t, srv, "import", "--file", file)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if !strings.Contains(out, "(March)") || !strings.Contains(out, "(April)") || strings.Count(out, "imported trip ") != 2 {
		t.Errorf("output = %q", out)
	}
}

func TestRunTripsImportStopsOnFirstError(t *testing.T) {
	t.Setenv("LINELEADER_SERVER", "")
	srv := testServer(t)
	file := filepath.Join(t.TempDir(), "trips.json")
	body := `[{"name":"Bad","stays":[{"resort":"R","check_in":"2025-03-01","check_out":"2025-03-05","points":50,"entry_ids":[99]}]},
 {"name":"Never","stays":[]}]`
	_ = os.WriteFile(file, []byte(body), 0o600)
	out, err := runTripsT(t, srv, "import", "--file", file)
	if err == nil || !strings.Contains(err.Error(), "Bad") {
		t.Fatalf("err = %v, want error naming the trip", err)
	}
	if strings.Contains(out, "imported") {
		t.Errorf("out = %q", out)
	}
}

func TestRunTripsImportRequiresFile(t *testing.T) {
	if _, err := runTripsT(t, "http://x", "import"); err == nil {
		t.Fatal("want error without --file")
	}
	if _, err := runTripsT(t, "http://x", "bogus"); err == nil {
		t.Fatal("want error for unknown subcommand")
	}
}
