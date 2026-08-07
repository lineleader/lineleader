package main

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lineleader/lineleader/internal/ledger"
)

func testDSN(t *testing.T) string {
	t.Helper()
	return ledger.OpenTestDSN(t)
}

func runLedgerT(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	err := runLedger(args, &buf)
	return buf.String(), err
}

func TestRunLedgerShowEmpty(t *testing.T) {
	db := testDSN(t)
	out, err := runLedgerT(t, "show", "--dsn", db)
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
	db := testDSN(t)
	if _, err := runLedgerT(t, "add", "--dsn", db, "--date", "2026-04-01", "--desc", "Point allocation", "--kind", "allocation", "--allotted", "120"); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := runLedgerT(t, "add", "--dsn", db, "--date", "2026-05-01", "--desc", "RIV 2br Std", "--kind", "usage", "--used", "80"); err != nil {
		t.Fatalf("add: %v", err)
	}

	out, err := runLedgerT(t, "show", "--dsn", db)
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
	db := testDSN(t)
	out, err := runLedgerT(t, "contracts", "add", "--dsn", db, "--name", "Point allocation", "--number", "1234567.000", "--resort", "VGF", "--points", "150", "--use-year-month", "Apr")
	if err != nil {
		t.Fatalf("contracts add: %v", err)
	}
	if !strings.Contains(out, "added contract 1 (Point allocation, 150 pts, use year April)") {
		t.Errorf("unexpected add output: %q", out)
	}

	out, err = runLedgerT(t, "contracts", "list", "--dsn", db)
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
	db := testDSN(t)

	out, err := runLedgerT(t, "add", "--dsn", db, "--date", "2026-04-01", "--desc", "Point allocation", "--kind", "allocation", "--allotted", "120")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if strings.TrimSpace(out) != "added entry 1" {
		t.Errorf("unexpected add output: %q", out)
	}

	out, err = runLedgerT(t, "edit", "--dsn", db, "--id", "1", "--date", "2026-04-02", "--desc", "Point allocation (edited)", "--kind", "allocation", "--allotted", "125")
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	if strings.TrimSpace(out) != "updated entry 1" {
		t.Errorf("unexpected edit output: %q", out)
	}

	s, err := ledger.Open(db)
	if err != nil {
		t.Fatalf("ledger.Open: %v", err)
	}
	entries, err := s.ListEntries()
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	if len(entries) != 1 || entries[0].Desc != "Point allocation (edited)" || entries[0].Allotted != 125 {
		t.Errorf("edit did not persist: %+v", entries)
	}
	s.Close()

	out, err = runLedgerT(t, "delete", "--dsn", db, "--id", "1")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if strings.TrimSpace(out) != "deleted entry 1" {
		t.Errorf("unexpected delete output: %q", out)
	}

	s, err = ledger.Open(db)
	if err != nil {
		t.Fatalf("ledger.Open: %v", err)
	}
	defer s.Close()
	entries, err = s.ListEntries()
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("delete did not persist: %+v", entries)
	}
}

func TestRunLedgerDistribute(t *testing.T) {
	db := testDSN(t)

	// DistributeNextYear targets time.Now().Year()+1, so seed the allocation at the
	// current year and assert against year+1 rather than a hardcoded year — this must
	// pass identically no matter what calendar year the test happens to run in.
	thisYear := time.Now().Year()
	nextYear := thisYear + 1
	seedDate := fmt.Sprintf("%04d-04-01", thisYear)

	if _, err := runLedgerT(t, "contracts", "add", "--dsn", db, "--name", "Point allocation", "--points", "120", "--use-year-month", "Apr"); err != nil {
		t.Fatalf("contracts add: %v", err)
	}

	if _, err := runLedgerT(t, "add", "--dsn", db, "--date", seedDate, "--desc", "Point allocation", "--kind", "allocation", "--allotted", "120", "--contract", "1"); err != nil {
		t.Fatalf("add: %v", err)
	}

	out, err := runLedgerT(t, "distribute", "--dsn", db)
	if err != nil {
		t.Fatalf("distribute: %v", err)
	}
	wantLine := fmt.Sprintf("distributed 120 pts to use year %d (Point allocation)", nextYear)
	if !strings.Contains(out, wantLine) {
		t.Errorf("unexpected distribute output: %q", out)
	}

	s, err := ledger.Open(db)
	if err != nil {
		t.Fatalf("ledger.Open: %v", err)
	}
	defer s.Close()
	entries, err := s.ListEntries()
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries len = %d, want 2 (original + distributed)", len(entries))
	}
	found := false
	for _, e := range entries {
		if e.UseYear == nextYear && e.Kind == ledger.KindAllocation && e.ContractID != nil && *e.ContractID == 1 {
			found = true
		}
	}
	if !found {
		t.Errorf("no distributed allocation row for use year %d found: %+v", nextYear, entries)
	}

	// Running distribute again is idempotent (no additional rows beyond next year).
	out2, err := runLedgerT(t, "distribute", "--dsn", db)
	if err != nil {
		t.Fatalf("second distribute: %v", err)
	}
	if !strings.Contains(out2, "nothing to distribute") {
		t.Errorf("expected idempotent no-op message, got: %q", out2)
	}
}

func TestRunLedgerUnknownSubcommand(t *testing.T) {
	_, err := runLedgerT(t, "bogus")
	if err == nil {
		t.Fatal("expected an error for an unknown ledger subcommand, got nil")
	}
}

func TestRunLedgerNoSubcommand(t *testing.T) {
	_, err := runLedgerT(t)
	if err == nil {
		t.Fatal("expected an error when no subcommand is given, got nil")
	}
}

func TestRunLedgerAddMissingRequiredFlags(t *testing.T) {
	db := testDSN(t)
	_, err := runLedgerT(t, "add", "--dsn", db)
	if err == nil {
		t.Fatal("expected an error when --date/--desc are missing, got nil")
	}
}

func TestRunLedgerContractsAddMissingRequiredFlags(t *testing.T) {
	db := testDSN(t)
	_, err := runLedgerT(t, "contracts", "add", "--dsn", db)
	if err == nil {
		t.Fatal("expected an error when required contract flags are missing, got nil")
	}
}

func TestRunLedgerAddBadDateFormat(t *testing.T) {
	db := testDSN(t)
	_, err := runLedgerT(t, "add", "--dsn", db, "--date", "not-a-date", "--desc", "whatever")
	if err == nil {
		t.Fatal("expected an error for a malformed --date, got nil")
	}
}

func TestRunLedgerDeleteMissingID(t *testing.T) {
	db := testDSN(t)
	_, err := runLedgerT(t, "delete", "--dsn", db)
	if err == nil {
		t.Fatal("expected an error when --id is missing, got nil")
	}
}
