package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lineleader/lineleader/internal/dvc"
	"github.com/lineleader/lineleader/internal/ledger"
)

func dateParse(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func newLedgerTestServer(t *testing.T) (*httptest.Server, *ledger.Store) {
	t.Helper()
	dir := t.TempDir()
	store, err := ledger.Open(filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatalf("Open ledger: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	srv := NewServer(Options{
		Charts:     []*dvc.ResortChart{minimalChart()},
		ConfigPath: filepath.Join(dir, "config.json"),
		PlansPath:  filepath.Join(dir, "plans.json"),
		Ledger:     store,
		Defaults:   Defaults{From: "2026-01-04", To: "2026-01-08", Budget: "100", MinNights: "1"},
	})
	return httptest.NewServer(srv), store
}

func TestLedgerPageRenders(t *testing.T) {
	srv, _ := newLedgerTestServer(t)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/ledger")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /ledger status = %d", resp.StatusCode)
	}
	got := body(t, resp)
	for _, want := range []string{"Points master ledger", "id=\"ledger-body\"", "Add entry", "Add contract"} {
		if !strings.Contains(got, want) {
			t.Errorf("page missing %q", want)
		}
	}
}

func TestLedgerAddAndDeleteEntry(t *testing.T) {
	srv, store := newLedgerTestServer(t)
	defer srv.Close()

	form := url.Values{
		"date":     {"2026-04-01"},
		"desc":     {"Point allocation"},
		"kind":     {ledger.KindAllocation},
		"allotted": {"120"},
	}
	resp, err := http.PostForm(srv.URL+"/ledger/entries", form)
	if err != nil {
		t.Fatal(err)
	}
	out := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("add status = %d", resp.StatusCode)
	}
	if !strings.Contains(out, "Point allocation") {
		t.Errorf("response missing the new entry; got:\n%s", out)
	}

	entries, _ := store.ListEntries()
	if len(entries) != 1 || entries[0].Allotted != 120 {
		t.Fatalf("store has %d entries, want 1 with 120 allotted", len(entries))
	}
	id := entries[0].ID

	// Delete it.
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/ledger/entries/"+strconv.FormatInt(id, 10), nil)
	dresp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body(t, dresp)
	if dresp.StatusCode != http.StatusOK {
		t.Fatalf("delete status = %d", dresp.StatusCode)
	}
	entries, _ = store.ListEntries()
	if len(entries) != 0 {
		t.Fatalf("after delete, store has %d entries, want 0", len(entries))
	}
}

func TestLedgerDistribute(t *testing.T) {
	srv, store := newLedgerTestServer(t)
	defer srv.Close()

	cid, _ := store.AddContract(ledger.Contract{Name: "Alloc", AnnualPoints: 120, UseYearMonth: 4})
	id := cid
	if _, err := store.AddEntry(ledger.Entry{
		UseYear: 2026, Date: dateParse(t, "2026-04-01"), Desc: "Alloc",
		Kind: ledger.KindAllocation, Allotted: 120, ContractID: &id,
	}); err != nil {
		t.Fatal(err)
	}

	resp, err := http.PostForm(srv.URL+"/ledger/distribute", nil)
	if err != nil {
		t.Fatal(err)
	}
	body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("distribute status = %d", resp.StatusCode)
	}
	entries, _ := store.ListEntries()
	if len(entries) != 2 {
		t.Fatalf("after distribute, %d entries, want 2", len(entries))
	}
}
