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

// TestLedgerPageRenders checks GET /ledger (the Recent view, and the
// default landing page per wireframe 2a): balance/recent-activity content is
// present, Contracts/History content is not, and the Recent nav link is the
// one marked active.
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
	for _, want := range []string{
		"Points ledger", "id=\"ledger-body\"", "Recent activity", "+ Add entry",
		`href="/ledger" class="active"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("page missing %q; got:\n%s", want, got)
		}
	}
	for _, notWant := range []string{"Add contract", "Per use year", "Distribute next year"} {
		if strings.Contains(got, notWant) {
			t.Errorf("Recent page should not contain History/Contracts content %q; got:\n%s", notWant, got)
		}
	}
}

// TestLedgerHistoryPageRenders checks GET /ledger/history: the per-use-year
// summary, full entry ledger and Distribute next year button are present,
// Recent/Contracts-only content is not, and History is the active nav link.
func TestLedgerHistoryPageRenders(t *testing.T) {
	srv, store := newLedgerTestServer(t)
	defer srv.Close()

	if _, err := store.AddEntry(ledger.Entry{
		UseYear: 2026, Date: dateParse(t, "2026-04-01"), Desc: "Original",
		Kind: ledger.KindAllocation, Allotted: 100,
	}); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Get(srv.URL + "/ledger/history")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /ledger/history status = %d", resp.StatusCode)
	}
	got := body(t, resp)
	for _, want := range []string{
		"Points ledger", "id=\"ledger-body\"", "Per use year", "Original",
		"Distribute next year", "Add entry",
		`href="/ledger/history" class="active"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("history page missing %q; got:\n%s", want, got)
		}
	}
	for _, notWant := range []string{"Recent activity", "Add contract"} {
		if strings.Contains(got, notWant) {
			t.Errorf("History page should not contain Recent/Contracts content %q; got:\n%s", notWant, got)
		}
	}
}

// TestLedgerContractsPageRenders checks GET /ledger/contracts: the contracts
// table and add-contract form are present, Recent/History-only content is
// not, and Contracts is the active nav link.
func TestLedgerContractsPageRenders(t *testing.T) {
	srv, store := newLedgerTestServer(t)
	defer srv.Close()

	if _, err := store.AddContract(ledger.Contract{Name: "BoardWalk", AnnualPoints: 150, UseYearMonth: time.April}); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Get(srv.URL + "/ledger/contracts")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /ledger/contracts status = %d", resp.StatusCode)
	}
	got := body(t, resp)
	for _, want := range []string{
		"Points ledger", "id=\"ledger-body\"", "BoardWalk", "Add contract",
		`href="/ledger/contracts" class="active"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("contracts page missing %q; got:\n%s", want, got)
		}
	}
	for _, notWant := range []string{"Recent activity", "Per use year", "Distribute next year"} {
		if strings.Contains(got, notWant) {
			t.Errorf("Contracts page should not contain Recent/History content %q; got:\n%s", notWant, got)
		}
	}
}

// TestLedgerAddEntryDefaultsToRecentView documents the view discriminator's
// default: a mutation request with no (or an unrecognised) "view" query
// param renders the Recent body rather than erroring.
func TestLedgerAddEntryDefaultsToRecentView(t *testing.T) {
	srv, _ := newLedgerTestServer(t)
	defer srv.Close()

	form := url.Values{
		"date":     {"2026-04-01"},
		"desc":     {"Unscoped add"},
		"kind":     {ledger.KindAllocation},
		"allotted": {"120"},
	}
	resp, err := http.PostForm(srv.URL+"/ledger/entries", form) // no ?view=
	if err != nil {
		t.Fatal(err)
	}
	out := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("add status = %d, body:\n%s", resp.StatusCode, out)
	}
	if !strings.Contains(out, "Recent activity") {
		t.Errorf("missing ?view= should default to the Recent body; got:\n%s", out)
	}
}

// TestLedgerAddAndDeleteEntry exercises "add" from the Recent view (its
// entry_add_form's disclosure lives there) and "delete" from the History
// view (delete-in-row only lives there), each with the ?view= that view's
// real controls would send, and checks the response swaps into that view.
func TestLedgerAddAndDeleteEntry(t *testing.T) {
	srv, store := newLedgerTestServer(t)
	defer srv.Close()

	form := url.Values{
		"date":     {"2026-04-01"},
		"desc":     {"Point allocation"},
		"kind":     {ledger.KindAllocation},
		"allotted": {"120"},
	}
	resp, err := http.PostForm(srv.URL+"/ledger/entries?view=recent", form)
	if err != nil {
		t.Fatal(err)
	}
	out := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("add status = %d", resp.StatusCode)
	}
	if !strings.Contains(out, "Recent activity") {
		t.Errorf("add from Recent should swap into the Recent body; got:\n%s", out)
	}
	if !strings.Contains(out, "Point allocation") {
		t.Errorf("response missing the new entry; got:\n%s", out)
	}

	entries, _ := store.ListEntries()
	if len(entries) != 1 || entries[0].Allotted != 120 {
		t.Fatalf("store has %d entries, want 1 with 120 allotted", len(entries))
	}
	id := entries[0].ID

	// Delete it, as the History view's row-delete button would.
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/ledger/entries/"+strconv.FormatInt(id, 10)+"?view=history", nil)
	dresp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	dout := body(t, dresp)
	if dresp.StatusCode != http.StatusOK {
		t.Fatalf("delete status = %d", dresp.StatusCode)
	}
	if !strings.Contains(dout, "Per use year") {
		t.Errorf("delete from History should swap into the History body; got:\n%s", dout)
	}
	if strings.Contains(dout, "Point allocation") {
		t.Errorf("deleted entry should no longer render; got:\n%s", dout)
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

	resp, err := http.Get(srv.URL + "/ledger/entries/" + strconv.FormatInt(id1, 10) + "/edit?view=history")
	if err != nil {
		t.Fatal(err)
	}
	out := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("edit status = %d, body:\n%s", resp.StatusCode, out)
	}
	if !strings.Contains(out, "Per use year") {
		t.Errorf("edit should swap into the History body; got:\n%s", out)
	}

	wantUpdateAction := `hx-post="/ledger/entries/` + strconv.FormatInt(id1, 10) + `/update?view=history"`
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
	wantOtherEditLink := `hx-get="/ledger/entries/` + strconv.FormatInt(id2, 10) + `/edit?view=history"`
	if !strings.Contains(out, wantOtherEditLink) {
		t.Errorf("other row should still render its Edit button %q; got:\n%s", wantOtherEditLink, out)
	}
	wantOtherUpdateAction := `hx-post="/ledger/entries/` + strconv.FormatInt(id2, 10) + `/update?view=history"`
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
	resp, err := http.PostForm(srv.URL+"/ledger/entries/"+strconv.FormatInt(id, 10)+"/update?view=history", form)
	if err != nil {
		t.Fatal(err)
	}
	out := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update status = %d, body:\n%s", resp.StatusCode, out)
	}
	if !strings.Contains(out, "Per use year") {
		t.Errorf("update should swap into the History body; got:\n%s", out)
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
	resp, err := http.PostForm(srv.URL+"/ledger/entries/"+strconv.FormatInt(id, 10)+"/update?view=history", form)
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
	if !strings.Contains(out, "Per use year") {
		t.Errorf("update error should still swap into the History body; got:\n%s", out)
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
	resp, err := http.PostForm(srv.URL+"/ledger/entries/"+strconv.FormatInt(id, 10)+"/update?view=history", form)
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

	resp, err := http.PostForm(srv.URL+"/ledger/entries/edit/cancel?view=history", nil)
	if err != nil {
		t.Fatal(err)
	}
	out := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("cancel status = %d, body:\n%s", resp.StatusCode, out)
	}
	if !strings.Contains(out, "Per use year") {
		t.Errorf("cancel should swap into the History body; got:\n%s", out)
	}
	if strings.Contains(out, "editing") {
		t.Errorf("cancel should restore normal row rendering; got:\n%s", out)
	}
	wantEditLink := `hx-get="/ledger/entries/` + strconv.FormatInt(id, 10) + `/edit?view=history"`
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
	resp, err := http.PostForm(srv.URL+"/ledger/contracts?view=contracts", form)
	if err != nil {
		t.Fatal(err)
	}
	out := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("add contract status = %d, body:\n%s", resp.StatusCode, out)
	}
	if !strings.Contains(out, "Add contract") {
		t.Errorf("add contract should swap into the Contracts body; got:\n%s", out)
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
	resp, err := http.PostForm(srv.URL+"/ledger/contracts?view=contracts", form)
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

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/ledger/contracts/"+strconv.FormatInt(cid, 10)+"?view=contracts", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	out := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete contract status = %d, body:\n%s", resp.StatusCode, out)
	}
	if !strings.Contains(out, "Add contract") {
		t.Errorf("delete contract should swap into the Contracts body; got:\n%s", out)
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

// TestLedgerHistoryHidesCostsWhenUnknown pins the ShowCosts guard's core
// promise: with the seeded dues series present (every store gets it, see
// ledger.Open) but no contract carrying priced cost data — the state of the
// real database right now, and the state newLedgerTestServer's contracts are
// always left in unless a test explicitly backfills them — CostBasis.Known()
// is false, so /ledger, /ledger/history and /ledger/contracts must render
// with no dollar sign anywhere, byte-identical in spirit to the page before
// this feature existed.
func TestLedgerHistoryHidesCostsWhenUnknown(t *testing.T) {
	srv, store := newLedgerTestServer(t)
	defer srv.Close()

	cid, err := store.AddContract(ledger.Contract{Name: "Unpriced", AnnualPoints: 120, UseYearMonth: time.April})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddEntry(ledger.Entry{
		UseYear: 2026, Date: dateParse(t, "2026-04-01"), Desc: "Alloc",
		Kind: ledger.KindAllocation, Allotted: 120, ContractID: &cid,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddEntry(ledger.Entry{
		UseYear: 2026, Date: dateParse(t, "2026-05-01"), Desc: "Trip",
		Kind: ledger.KindUsage, Used: 40, ContractID: &cid,
	}); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{"/ledger", "/ledger/history", "/ledger/contracts"} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		out := body(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s status = %d", path, resp.StatusCode)
		}
		if strings.Contains(out, "$") {
			t.Errorf("GET %s should render no dollar sign while CostBasis is unknown; got:\n%s", path, out)
		}
		if strings.Contains(out, "Total spent") {
			t.Errorf("GET %s should not show the Total spent footer while CostBasis is unknown; got:\n%s", path, out)
		}
	}
}

// TestLedgerHistoryShowsCostPerEntryAndUseYear backfills one contract's cost
// fields directly through the store (contract editing arrives in a later
// commit), then checks GET /ledger/history prices the usage entry against
// that contract's own rate, leaves the allocation row's cost blank, and
// totals the per-use-year summary and the ledger-wide "Total spent" footer.
//
// Golden values reuse cost_store_test.go's contract: 120 pts/yr, 44-year
// term, $29,400 purchase + $588.35 closing -> $5.6796/pt/yr = 5_679_612
// micros (RateFor). Dues for use year 2026 (seeded) is 8_223_500 micros
// ($8.2235/pt). 40 points used: PointCost(40, 5_679_612, 8_223_500) =
// divRound((5_679_612+8_223_500)*40, 10_000) = divRound(556_124_480, 10_000)
// = 55_612 cents = "$556.12".
func TestLedgerHistoryShowsCostPerEntryAndUseYear(t *testing.T) {
	srv, store := newLedgerTestServer(t)
	defer srv.Close()

	cid, err := store.AddContract(ledger.Contract{
		Name: "Point allocation", AnnualPoints: 120, UseYearMonth: time.April,
		TermYears: 44, PurchasePrice: 2_940_000, ClosingCosts: 58_835,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddEntry(ledger.Entry{
		UseYear: 2026, Date: dateParse(t, "2026-04-01"), Desc: "Alloc",
		Kind: ledger.KindAllocation, Allotted: 120, ContractID: &cid,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddEntry(ledger.Entry{
		UseYear: 2026, Date: dateParse(t, "2026-05-01"), Desc: "Priced trip",
		Kind: ledger.KindUsage, Used: 40, ContractID: &cid,
	}); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Get(srv.URL + "/ledger/history")
	if err != nil {
		t.Fatal(err)
	}
	out := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /ledger/history status = %d, body:\n%s", resp.StatusCode, out)
	}
	for _, want := range []string{"$556.12", "Total spent"} {
		if !strings.Contains(out, want) {
			t.Errorf("history page missing %q; got:\n%s", want, out)
		}
	}
}

// TestLedgerRecentShowsCostsWhenKnown backfills a contract's cost data, adds
// a priced usage entry, and checks GET /ledger (the Recent view) prices the
// recent-activity row, the spent-by-year widget, and shows an approximate
// dollar balance. Golden values reuse
// TestLedgerHistoryShowsCostPerEntryAndUseYear's contract and entry: 40
// points used in UY2026 against the 2019-style contract prices to $556.12
// (see that test's comment for the arithmetic).
//
// The balance valuation uses the blended rate (nil contract id), not the
// entry's own contract rate, and the current use year — with only one
// contract in play here, blended == that contract's own rate, so the
// running balance of 80 points also prices to the same per-point rate:
// PointCost(80, 5_679_612, 8_223_500) = divRound(1_112_248_960, 10_000) =
// 111_225 cents = "$1,112.25".
func TestLedgerRecentShowsCostsWhenKnown(t *testing.T) {
	srv, store := newLedgerTestServer(t)
	defer srv.Close()

	cid, err := store.AddContract(ledger.Contract{
		Name: "Point allocation", AnnualPoints: 120, UseYearMonth: time.April,
		TermYears: 44, PurchasePrice: 2_940_000, ClosingCosts: 58_835,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddEntry(ledger.Entry{
		UseYear: 2026, Date: dateParse(t, "2026-04-01"), Desc: "Alloc",
		Kind: ledger.KindAllocation, Allotted: 120, ContractID: &cid,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddEntry(ledger.Entry{
		UseYear: 2026, Date: dateParse(t, "2026-05-01"), Desc: "Priced trip",
		Kind: ledger.KindUsage, Used: 40, ContractID: &cid,
	}); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Get(srv.URL + "/ledger")
	if err != nil {
		t.Fatal(err)
	}
	out := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /ledger status = %d, body:\n%s", resp.StatusCode, out)
	}
	for _, want := range []string{"$556.12", "$1,112.25"} {
		if !strings.Contains(out, want) {
			t.Errorf("recent page missing %q; got:\n%s", want, out)
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

	resp, err := http.PostForm(srv.URL+"/ledger/distribute?view=history", nil)
	if err != nil {
		t.Fatal(err)
	}
	out := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("distribute status = %d", resp.StatusCode)
	}
	if !strings.Contains(out, "Per use year") {
		t.Errorf("distribute should swap into the History body; got:\n%s", out)
	}
	entries, _ := store.ListEntries()
	if len(entries) != 2 {
		t.Fatalf("after distribute, %d entries, want 2", len(entries))
	}
}
