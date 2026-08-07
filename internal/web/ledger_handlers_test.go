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
	store := ledger.OpenTest(t)
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

func TestLedgerEditEntryRendersFormRow(t *testing.T) {
	srv, store := newLedgerTestServer(t)
	defer srv.Close()

	id1, err := store.AddEntry(ledger.Entry{
		UseYear: 2026, Date: dateParse(t, "2026-04-01"), Desc: "First entry",
		Kind: ledger.KindAllocation, Allotted: 100, Tag: "t1",
	})
	if err != nil {
		t.Fatal(err)
	}
	id2, err := store.AddEntry(ledger.Entry{
		UseYear: 2026, Date: dateParse(t, "2026-04-05"), Desc: "Second entry",
		Kind: ledger.KindUsage, Used: 20, Tag: "t2",
	})
	if err != nil {
		t.Fatal(err)
	}

	resp, err := http.Get(srv.URL + "/ledger/entries/" + strconv.FormatInt(id1, 10) + "/edit")
	if err != nil {
		t.Fatal(err)
	}
	out := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("edit status = %d, body:\n%s", resp.StatusCode, out)
	}

	wantUpdateAction := `hx-post="/ledger/entries/` + strconv.FormatInt(id1, 10) + `/update"`
	if !strings.Contains(out, wantUpdateAction) {
		t.Errorf("response missing edit-form action %q; got:\n%s", wantUpdateAction, out)
	}
	for _, want := range []string{
		`value="First entry"`,
		`value="2026-04-01"`,
		`value="100"`,
		`value="t1"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("edit form missing %q; got:\n%s", want, out)
		}
	}

	// The other row must remain in normal (non-form) rendering.
	if !strings.Contains(out, "Second entry") {
		t.Errorf("response missing untouched row content; got:\n%s", out)
	}
	wantOtherEditLink := `hx-get="/ledger/entries/` + strconv.FormatInt(id2, 10) + `/edit"`
	if !strings.Contains(out, wantOtherEditLink) {
		t.Errorf("other row should still render its Edit button %q; got:\n%s", wantOtherEditLink, out)
	}
	wantOtherUpdateAction := `hx-post="/ledger/entries/` + strconv.FormatInt(id2, 10) + `/update"`
	if strings.Contains(out, wantOtherUpdateAction) {
		t.Errorf("other row should not be rendered as an edit form; got:\n%s", out)
	}
}

func TestLedgerUpdateEntryPersists(t *testing.T) {
	srv, store := newLedgerTestServer(t)
	defer srv.Close()

	cid, err := store.AddContract(ledger.Contract{Name: "Linked", AnnualPoints: 100, UseYearMonth: time.April})
	if err != nil {
		t.Fatal(err)
	}
	id, err := store.AddEntry(ledger.Entry{
		UseYear: 2026, Date: dateParse(t, "2026-04-01"), Desc: "Original",
		Kind: ledger.KindAllocation, Allotted: 100, Tag: "orig",
	})
	if err != nil {
		t.Fatal(err)
	}

	form := url.Values{
		"year":     {"2026"},
		"date":     {"2026-05-15"},
		"desc":     {"Updated desc"},
		"kind":     {ledger.KindBonus},
		"allotted": {"75"},
		"used":     {"0"},
		"tag":      {"changed"},
		"contract": {strconv.FormatInt(cid, 10)},
	}
	resp, err := http.PostForm(srv.URL+"/ledger/entries/"+strconv.FormatInt(id, 10)+"/update", form)
	if err != nil {
		t.Fatal(err)
	}
	out := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update status = %d, body:\n%s", resp.StatusCode, out)
	}
	if !strings.Contains(out, "Updated desc") {
		t.Errorf("response missing updated desc; got:\n%s", out)
	}
	// Should leave edit mode: no edit-form action left in the fragment.
	if strings.Contains(out, "editing") {
		t.Errorf("response should not still be in edit mode; got:\n%s", out)
	}

	entries, err := store.ListEntries()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("store has %d entries, want 1", len(entries))
	}
	got := entries[0]
	if got.Desc != "Updated desc" || got.Kind != ledger.KindBonus || got.Allotted != 75 || got.Tag != "changed" {
		t.Errorf("persisted entry = %+v, want desc=Updated desc kind=%s allotted=75 tag=changed", got, ledger.KindBonus)
	}
	if !got.Date.Equal(dateParse(t, "2026-05-15")) {
		t.Errorf("persisted date = %v, want 2026-05-15", got.Date)
	}
	if got.ContractID == nil || *got.ContractID != cid {
		t.Errorf("persisted ContractID = %v, want %d", got.ContractID, cid)
	}
}

func TestLedgerUpdateEntryBadDateShowsError(t *testing.T) {
	srv, store := newLedgerTestServer(t)
	defer srv.Close()

	id, err := store.AddEntry(ledger.Entry{
		UseYear: 2026, Date: dateParse(t, "2026-04-01"), Desc: "Original",
		Kind: ledger.KindAllocation, Allotted: 100,
	})
	if err != nil {
		t.Fatal(err)
	}

	form := url.Values{
		"date": {"not-a-date"},
		"desc": {"Should not persist"},
		"kind": {ledger.KindBonus},
	}
	resp, err := http.PostForm(srv.URL+"/ledger/entries/"+strconv.FormatInt(id, 10)+"/update", form)
	if err != nil {
		t.Fatal(err)
	}
	out := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update status = %d, body:\n%s", resp.StatusCode, out)
	}
	if !strings.Contains(out, "err") {
		t.Errorf("response should surface the parse error; got:\n%s", out)
	}

	entries, err := store.ListEntries()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Desc != "Original" {
		t.Fatalf("entry should be unchanged after bad date, got %+v", entries)
	}
}

func TestLedgerUpdateEntryStoreErrorShowsError(t *testing.T) {
	srv, store := newLedgerTestServer(t)
	defer srv.Close()

	id, err := store.AddEntry(ledger.Entry{
		UseYear: 2026, Date: dateParse(t, "2026-04-01"), Desc: "Original",
		Kind: ledger.KindAllocation, Allotted: 100,
	})
	if err != nil {
		t.Fatal(err)
	}

	// A contract id that does not exist violates the entries.contract_id FK,
	// forcing store.UpdateEntry to return an error.
	form := url.Values{
		"date":     {"2026-04-01"},
		"desc":     {"Should not persist"},
		"kind":     {ledger.KindBonus},
		"contract": {"999999"},
	}
	resp, err := http.PostForm(srv.URL+"/ledger/entries/"+strconv.FormatInt(id, 10)+"/update", form)
	if err != nil {
		t.Fatal(err)
	}
	out := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update status = %d, body:\n%s", resp.StatusCode, out)
	}
	if !strings.Contains(out, "FOREIGN KEY") && !strings.Contains(out, "constraint") {
		t.Errorf("response should surface the store error; got:\n%s", out)
	}

	entries, err := store.ListEntries()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Desc != "Original" {
		t.Fatalf("entry should be unchanged after store error, got %+v", entries)
	}
}

func TestLedgerBadPathID(t *testing.T) {
	srv, _ := newLedgerTestServer(t)
	defer srv.Close()

	cases := []struct {
		name   string
		method string
		url    string
	}{
		{"edit", http.MethodGet, srv.URL + "/ledger/entries/not-a-number/edit"},
		{"update", http.MethodPost, srv.URL + "/ledger/entries/not-a-number/update"},
		{"delete-contract", http.MethodDelete, srv.URL + "/ledger/contracts/not-a-number"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req, err := http.NewRequest(c.method, c.url, nil)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			body(t, resp)
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("%s: status = %d, want 400", c.name, resp.StatusCode)
			}
		})
	}
}

func TestLedgerCancelEdit(t *testing.T) {
	srv, store := newLedgerTestServer(t)
	defer srv.Close()

	id, err := store.AddEntry(ledger.Entry{
		UseYear: 2026, Date: dateParse(t, "2026-04-01"), Desc: "Some entry",
		Kind: ledger.KindAllocation, Allotted: 100,
	})
	if err != nil {
		t.Fatal(err)
	}

	resp, err := http.PostForm(srv.URL+"/ledger/entries/edit/cancel", nil)
	if err != nil {
		t.Fatal(err)
	}
	out := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("cancel status = %d, body:\n%s", resp.StatusCode, out)
	}
	if strings.Contains(out, "editing") {
		t.Errorf("cancel should restore normal row rendering; got:\n%s", out)
	}
	wantEditLink := `hx-get="/ledger/entries/` + strconv.FormatInt(id, 10) + `/edit"`
	if !strings.Contains(out, wantEditLink) {
		t.Errorf("cancel response missing normal Edit button %q; got:\n%s", wantEditLink, out)
	}
}

func TestLedgerAddContract(t *testing.T) {
	srv, store := newLedgerTestServer(t)
	defer srv.Close()

	form := url.Values{
		"name":           {"BoardWalk"},
		"number":         {"12345"},
		"resort":         {"BWV"},
		"points":         {"150"},
		"use_year_month": {"April"},
	}
	resp, err := http.PostForm(srv.URL+"/ledger/contracts", form)
	if err != nil {
		t.Fatal(err)
	}
	out := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("add contract status = %d, body:\n%s", resp.StatusCode, out)
	}
	if !strings.Contains(out, "BoardWalk") {
		t.Errorf("response missing new contract; got:\n%s", out)
	}

	contracts, err := store.ListContracts()
	if err != nil {
		t.Fatal(err)
	}
	if len(contracts) != 1 {
		t.Fatalf("store has %d contracts, want 1", len(contracts))
	}
	c := contracts[0]
	if c.Name != "BoardWalk" || c.Number != "12345" || c.HomeResort != "BWV" || c.AnnualPoints != 150 || c.UseYearMonth != time.April {
		t.Errorf("persisted contract = %+v, want name=BoardWalk number=12345 resort=BWV points=150 month=April", c)
	}
}

func TestLedgerAddContractBadMonth(t *testing.T) {
	srv, store := newLedgerTestServer(t)
	defer srv.Close()

	form := url.Values{
		"name":           {"Bad"},
		"number":         {"999"},
		"resort":         {"XYZ"},
		"points":         {"100"},
		"use_year_month": {"Notamonth"},
	}
	resp, err := http.PostForm(srv.URL+"/ledger/contracts", form)
	if err != nil {
		t.Fatal(err)
	}
	out := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("bad-month add status = %d, body:\n%s", resp.StatusCode, out)
	}
	if !strings.Contains(out, "invalid month") {
		t.Errorf("response should surface parseMonth error; got:\n%s", out)
	}

	contracts, err := store.ListContracts()
	if err != nil {
		t.Fatal(err)
	}
	if len(contracts) != 0 {
		t.Fatalf("store has %d contracts, want 0 after rejected add", len(contracts))
	}
}

func TestLedgerDeleteContract(t *testing.T) {
	srv, store := newLedgerTestServer(t)
	defer srv.Close()

	cid, err := store.AddContract(ledger.Contract{Name: "Old Key", AnnualPoints: 100, UseYearMonth: time.October})
	if err != nil {
		t.Fatal(err)
	}

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/ledger/contracts/"+strconv.FormatInt(cid, 10), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	out := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete contract status = %d, body:\n%s", resp.StatusCode, out)
	}
	if strings.Contains(out, "Old Key") {
		t.Errorf("response should no longer list deleted contract; got:\n%s", out)
	}

	contracts, err := store.ListContracts()
	if err != nil {
		t.Fatal(err)
	}
	if len(contracts) != 0 {
		t.Fatalf("store has %d contracts, want 0 after delete", len(contracts))
	}
}

func TestParseMonth(t *testing.T) {
	cases := []struct {
		in      string
		want    time.Month
		wantErr bool
	}{
		{"April", time.April, false},
		{"apr", time.April, false},
		{"APR", time.April, false},
		{"december", time.December, false},
		{"4", time.April, false},
		{"12", time.December, false},
		{"1", time.January, false},
		{"0", 0, true},
		{"13", 0, true},
		{"notamonth", 0, true},
		{"", 0, true},
	}
	for _, c := range cases {
		got, err := parseMonth(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseMonth(%q) = %v, nil; want error", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseMonth(%q) unexpected error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("parseMonth(%q) = %v, want %v", c.in, got, c.want)
		}
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
