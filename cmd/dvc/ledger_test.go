package main

import (
	"bytes"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lineleader/lineleader/internal/dvc"
	"github.com/lineleader/lineleader/internal/ledger"
	"github.com/lineleader/lineleader/internal/web"
)

// minimalChart is a tiny chart so web.NewServer has something to load; the
// ledger subcommands under test never touch trip-planning routes.
func minimalChart() *dvc.ResortChart {
	return &dvc.ResortChart{
		ResortName: "Test Resort",
		ResortCode: "TST",
		Year:       2026,
		Columns: []dvc.Column{
			{RoomType: "STUDIO", View: "R", Sleeps: 4},
		},
		Seasons: []dvc.Season{
			{
				Periods: []dvc.DateRange{{Start: "2026-01-01", End: "2026-01-31"}},
				SunThu:  []int{10},
				FriSat:  []int{14},
			},
		},
	}
}

// testServer boots a real API server (internal/web, backed by a fresh
// Postgres schema via ledger.OpenTest) and returns its base URL. This is the
// server the CLI now talks to over HTTP instead of opening a database
// itself — see docs/pitches/hosted-lineleader.md, "4. CLI becomes a thin
// client".
func testServer(t *testing.T) string {
	t.Helper()
	store := ledger.OpenTest(t)
	dir := t.TempDir()
	srv := web.NewServer(web.Options{
		Charts:     []*dvc.ResortChart{minimalChart()},
		ConfigPath: filepath.Join(dir, "config.json"),
		PlansPath:  filepath.Join(dir, "plans.json"),
		Ledger:     store,
		Defaults:   web.Defaults{From: "2026-01-04", To: "2026-01-08", Budget: "100", MinNights: "1"},
	})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return ts.URL
}

func runLedgerT(t *testing.T, server string, args ...string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	full := append([]string{}, args...)
	full = append(full, "--server", server)
	err := runLedger(full, &buf)
	return buf.String(), err
}

func TestRunLedgerShowEmpty(t *testing.T) {
	srv := testServer(t)
	out, err := runLedgerT(t, srv, "show")
	if err != nil {
		t.Fatalf("runLedger show: %v", err)
	}
	for _, col := range []string{"ID", "YEAR", "DATE", "DESC", "ALLOTTED", "USED", "TOTAL", "TAG"} {
		if !strings.Contains(out, col) {
			t.Errorf("output missing header column %q, got:\n%s", col, out)
		}
	}
	if strings.Contains(out, "Per use year:") {
		t.Errorf("empty ledger should not print a per-use-year rollup, got:\n%s", out)
	}
}

func TestRunLedgerShowWithEntries(t *testing.T) {
	srv := testServer(t)
	if _, err := runLedgerT(t, srv, "add", "--date", "2026-04-01", "--desc", "Point allocation", "--kind", "allocation", "--allotted", "120"); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := runLedgerT(t, srv, "add", "--date", "2026-05-01", "--desc", "RIV 2br Std", "--kind", "usage", "--used", "80"); err != nil {
		t.Fatalf("add: %v", err)
	}

	out, err := runLedgerT(t, srv, "show")
	if err != nil {
		t.Fatalf("runLedger show: %v", err)
	}
	if !strings.Contains(out, "Point allocation") || !strings.Contains(out, "RIV 2br Std") {
		t.Errorf("output missing entries, got:\n%s", out)
	}
	if !strings.Contains(out, "Per use year:") {
		t.Errorf("output missing per-use-year rollup section, got:\n%s", out)
	}
	for _, col := range []string{"YEAR", "ALLOTTED", "USED", "NET"} {
		if !strings.Contains(out, col) {
			t.Errorf("output missing rollup header column %q, got:\n%s", col, out)
		}
	}
}

func TestRunLedgerContractsAddAndList(t *testing.T) {
	srv := testServer(t)
	out, err := runLedgerT(t, srv, "contracts", "add", "--name", "Point allocation", "--number", "1234567.000", "--resort", "VGF", "--points", "150", "--use-year-month", "Apr")
	if err != nil {
		t.Fatalf("contracts add: %v", err)
	}
	if !strings.Contains(out, "added contract 1 (Point allocation, 150 pts, use year April)") {
		t.Errorf("unexpected add output: %q", out)
	}

	out, err = runLedgerT(t, srv, "contracts", "list")
	if err != nil {
		t.Fatalf("contracts list: %v", err)
	}
	for _, col := range []string{"ID", "NAME", "NUMBER", "RESORT", "POINTS", "USE-YEAR"} {
		if !strings.Contains(out, col) {
			t.Errorf("missing list header column %q, got:\n%s", col, out)
		}
	}
	if !strings.Contains(out, "Point allocation") || !strings.Contains(out, "VGF") || !strings.Contains(out, "150") {
		t.Errorf("missing contract row, got:\n%s", out)
	}
}

func TestRunLedgerAddEditDelete(t *testing.T) {
	srv := testServer(t)

	out, err := runLedgerT(t, srv, "add", "--date", "2026-04-01", "--desc", "Point allocation", "--kind", "allocation", "--allotted", "120")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if strings.TrimSpace(out) != "added entry 1" {
		t.Errorf("unexpected add output: %q", out)
	}

	out, err = runLedgerT(t, srv, "edit", "--id", "1", "--date", "2026-04-02", "--desc", "Point allocation (edited)", "--kind", "allocation", "--allotted", "125")
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	if strings.TrimSpace(out) != "updated entry 1" {
		t.Errorf("unexpected edit output: %q", out)
	}

	out, err = runLedgerT(t, srv, "show")
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	if !strings.Contains(out, "Point allocation (edited)") || !strings.Contains(out, "125") {
		t.Errorf("edit did not persist: %s", out)
	}

	out, err = runLedgerT(t, srv, "delete", "--id", "1")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if strings.TrimSpace(out) != "deleted entry 1" {
		t.Errorf("unexpected delete output: %q", out)
	}

	out, err = runLedgerT(t, srv, "show")
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	if strings.Contains(out, "Point allocation (edited)") {
		t.Errorf("delete did not persist: %s", out)
	}
}

func TestRunLedgerEditPartial_KeepsUnsetFields(t *testing.T) {
	srv := testServer(t)

	if _, err := runLedgerT(t, srv, "add", "--date", "2026-04-01", "--desc", "Point allocation", "--kind", "allocation", "--allotted", "120", "--tag", "Bank"); err != nil {
		t.Fatalf("add: %v", err)
	}

	// Only --allotted is set; every other field (desc, date, kind, tag) must
	// survive untouched, since edit is a read-modify-write over just the
	// flags the user actually passed.
	out, err := runLedgerT(t, srv, "edit", "--id", "1", "--allotted", "200")
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	if strings.TrimSpace(out) != "updated entry 1" {
		t.Errorf("unexpected edit output: %q", out)
	}

	out, err = runLedgerT(t, srv, "show")
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	if !strings.Contains(out, "Point allocation") || !strings.Contains(out, "Bank") || !strings.Contains(out, "200") {
		t.Errorf("partial edit lost untouched fields: %s", out)
	}
}

func TestRunLedgerEditUnknownID_NotFound(t *testing.T) {
	srv := testServer(t)
	_, err := runLedgerT(t, srv, "edit", "--id", "999", "--desc", "x")
	if err == nil {
		t.Fatal("expected an error editing an unknown entry id, got nil")
	}
	if !strings.Contains(err.Error(), "999") || !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected a 'not found' error mentioning the id, got: %v", err)
	}
}

func TestRunLedgerDistribute(t *testing.T) {
	srv := testServer(t)

	// DistributeNextYear targets time.Now().Year()+1, so seed the allocation at the
	// current year and assert against year+1 rather than a hardcoded year — this must
	// pass identically no matter what calendar year the test happens to run in.
	thisYear := time.Now().Year()
	nextYear := thisYear + 1
	seedDate := fmt.Sprintf("%04d-04-01", thisYear)

	if _, err := runLedgerT(t, srv, "contracts", "add", "--name", "Point allocation", "--points", "120", "--use-year-month", "Apr"); err != nil {
		t.Fatalf("contracts add: %v", err)
	}

	if _, err := runLedgerT(t, srv, "add", "--date", seedDate, "--desc", "Point allocation", "--kind", "allocation", "--allotted", "120", "--contract", "1"); err != nil {
		t.Fatalf("add: %v", err)
	}

	out, err := runLedgerT(t, srv, "distribute")
	if err != nil {
		t.Fatalf("distribute: %v", err)
	}
	wantLine := fmt.Sprintf("distributed 120 pts to use year %d (Point allocation)", nextYear)
	if !strings.Contains(out, wantLine) {
		t.Errorf("unexpected distribute output: %q", out)
	}

	out, err = runLedgerT(t, srv, "show")
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	if strings.Count(out, "Point allocation") < 2 {
		t.Fatalf("expected original + distributed rows in show output, got:\n%s", out)
	}
	if !strings.Contains(out, fmt.Sprintf("%d", nextYear)) {
		t.Errorf("expected a row for use year %d, got:\n%s", nextYear, out)
	}

	// Running distribute again is idempotent (no additional rows beyond next year).
	out2, err := runLedgerT(t, srv, "distribute")
	if err != nil {
		t.Fatalf("second distribute: %v", err)
	}
	if !strings.Contains(out2, "nothing to distribute") {
		t.Errorf("expected idempotent no-op message, got: %q", out2)
	}
}

func TestRunLedgerUnknownSubcommand(t *testing.T) {
	_, err := runLedgerT(t, "http://unused.invalid", "bogus")
	if err == nil {
		t.Fatal("expected an error for an unknown ledger subcommand, got nil")
	}
}

func TestRunLedgerNoSubcommand(t *testing.T) {
	var buf bytes.Buffer
	err := runLedger(nil, &buf)
	if err == nil {
		t.Fatal("expected an error when no subcommand is given, got nil")
	}
}

func TestRunLedgerAddMissingRequiredFlags(t *testing.T) {
	srv := testServer(t)
	_, err := runLedgerT(t, srv, "add")
	if err == nil {
		t.Fatal("expected an error when --date/--desc are missing, got nil")
	}
}

func TestRunLedgerContractsAddMissingRequiredFlags(t *testing.T) {
	srv := testServer(t)
	_, err := runLedgerT(t, srv, "contracts", "add")
	if err == nil {
		t.Fatal("expected an error when required contract flags are missing, got nil")
	}
}

func TestRunLedgerAddBadDateFormat(t *testing.T) {
	srv := testServer(t)
	_, err := runLedgerT(t, srv, "add", "--date", "not-a-date", "--desc", "whatever")
	if err == nil {
		t.Fatal("expected an error for a malformed --date, got nil")
	}
}

func TestRunLedgerDeleteMissingID(t *testing.T) {
	srv := testServer(t)
	_, err := runLedgerT(t, srv, "delete")
	if err == nil {
		t.Fatal("expected an error when --id is missing, got nil")
	}
}

func TestRunLedgerNoServerConfigured(t *testing.T) {
	// No --server flag, and (in a normal test environment) no
	// LINELEADER_SERVER env var or client.json either: resolving the client
	// config must fail with a clear, actionable error instead of e.g. a nil
	// pointer or an opaque connection error.
	t.Setenv("LINELEADER_SERVER", "")
	t.Setenv("LINELEADER_TOKEN", "")
	t.Setenv("HOME", t.TempDir()) // no real ~/.config/lineleader/client.json
	t.Setenv("XDG_CONFIG_HOME", "")

	var buf bytes.Buffer
	err := runLedger([]string{"show"}, &buf)
	if err == nil {
		t.Fatal("expected an error when no server is configured, got nil")
	}
	if !strings.Contains(err.Error(), "server") {
		t.Errorf("expected error to mention the server config, got: %v", err)
	}
}
