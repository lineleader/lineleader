package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lineleader/lineleader/internal/dvc"
	"github.com/lineleader/lineleader/internal/ledger"
)

// apiEntryDTO mirrors the wire shape of an entry response for test decoding.
type apiEntryDTO struct {
	ID             int64  `json:"id"`
	UseYear        int    `json:"use_year"`
	Date           string `json:"date"`
	Desc           string `json:"description"`
	Kind           string `json:"kind"`
	Allotted       int    `json:"allotted"`
	Used           int    `json:"used"`
	Tag            string `json:"tag"`
	ContractID     *int64 `json:"contract_id"`
	RunningBalance *int   `json:"running_balance"`
}

type apiContractDTO struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Number       string `json:"number"`
	HomeResort   string `json:"home_resort"`
	AnnualPoints int    `json:"annual_points"`
	UseYearMonth string `json:"use_year_month"`
}

type apiSummaryDTO struct {
	UseYear      int  `json:"use_year"`
	Allotted     int  `json:"allotted"`
	Used         int  `json:"used"`
	Net          int  `json:"net"`
	OverBorrowed bool `json:"over_borrowed"`
}

type apiErrorDTO struct {
	Error string `json:"error"`
}

func newAuthAPITestServer(t *testing.T, secret string) (*httptest.Server, *ledger.Store) {
	t.Helper()
	dir := t.TempDir()
	store := ledger.OpenTest(t)
	srv := NewServer(Options{
		Charts:     []*dvc.ResortChart{minimalChart()},
		ConfigPath: filepath.Join(dir, "config.json"),
		Ledger:     store,
		Defaults:   Defaults{From: "2026-01-04", To: "2026-01-08", Budget: "100", MinNights: "1"},
		AuthSecret: secret,
	})
	return httptest.NewServer(srv), store
}

func decodeJSON(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("decode JSON response: %v", err)
	}
}

func postJSON(t *testing.T, url, body string) *http.Response {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func putJSON(t *testing.T, url, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func deleteReq(t *testing.T, url string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestAPIListEntries_Empty(t *testing.T) {
	srv, _ := newLedgerTestServer(t)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/ledger/entries")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got []apiEntryDTO
	decodeJSON(t, resp, &got)
	if len(got) != 0 {
		t.Fatalf("entries = %v, want empty", got)
	}
}

func TestAPIAddEntry_CreatedAndDefaultsUseYear(t *testing.T) {
	srv, store := newLedgerTestServer(t)
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/v1/ledger/entries",
		`{"date":"2026-04-01","description":"Point allocation","kind":"allocation","allotted":120}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	var got apiEntryDTO
	decodeJSON(t, resp, &got)
	if got.ID == 0 {
		t.Errorf("ID = 0, want nonzero")
	}
	if got.UseYear != 2026 {
		t.Errorf("UseYear = %d, want 2026 (defaulted from date)", got.UseYear)
	}
	if got.Date != "2026-04-01" || got.Desc != "Point allocation" || got.Kind != ledger.KindAllocation || got.Allotted != 120 {
		t.Errorf("got = %+v", got)
	}
	if got.ContractID != nil {
		t.Errorf("ContractID = %v, want nil", got.ContractID)
	}

	entries, err := store.ListEntries()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Allotted != 120 {
		t.Fatalf("store has %d entries, want 1 with 120 allotted", len(entries))
	}
}

func TestAPIAddEntry_DefaultsKindWhenEmpty(t *testing.T) {
	srv, store := newLedgerTestServer(t)
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/v1/ledger/entries", `{"date":"2026-04-01","description":"Trip"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	var got apiEntryDTO
	decodeJSON(t, resp, &got)
	if got.Kind != ledger.KindUsage {
		t.Errorf("Kind = %q, want default %q", got.Kind, ledger.KindUsage)
	}

	entries, _ := store.ListEntries()
	if len(entries) != 1 || entries[0].Kind != ledger.KindUsage {
		t.Fatalf("store entry kind = %q, want %q", entries[0].Kind, ledger.KindUsage)
	}
}

func TestAPIAddEntry_MissingDate_400(t *testing.T) {
	srv, _ := newLedgerTestServer(t)
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/v1/ledger/entries", `{"description":"no date"}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var got apiErrorDTO
	decodeJSON(t, resp, &got)
	if got.Error == "" {
		t.Errorf("expected non-empty error message")
	}
}

func TestAPIAddEntry_InvalidKind_400(t *testing.T) {
	srv, _ := newLedgerTestServer(t)
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/v1/ledger/entries",
		`{"date":"2026-04-01","description":"x","kind":"not-a-kind"}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var got apiErrorDTO
	decodeJSON(t, resp, &got)
	if !strings.Contains(got.Error, "kind") {
		t.Errorf("error = %q, want it to mention kind", got.Error)
	}
}

func TestAPIAddEntry_InvalidJSON_400(t *testing.T) {
	srv, _ := newLedgerTestServer(t)
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/v1/ledger/entries", `{not json`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestAPIListEntries_IncludesRunningBalance(t *testing.T) {
	srv, store := newLedgerTestServer(t)
	defer srv.Close()

	if _, err := store.AddEntry(ledger.Entry{
		UseYear: 2026, Date: dateParse(t, "2026-04-01"), Desc: "Alloc",
		Kind: ledger.KindAllocation, Allotted: 100,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddEntry(ledger.Entry{
		UseYear: 2026, Date: dateParse(t, "2026-04-05"), Desc: "Trip",
		Kind: ledger.KindUsage, Used: 30,
	}); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Get(srv.URL + "/api/v1/ledger/entries")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var got []apiEntryDTO
	decodeJSON(t, resp, &got)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].RunningBalance == nil || *got[0].RunningBalance != 100 {
		t.Errorf("entry[0].RunningBalance = %v, want 100", got[0].RunningBalance)
	}
	if got[1].RunningBalance == nil || *got[1].RunningBalance != 70 {
		t.Errorf("entry[1].RunningBalance = %v, want 70", got[1].RunningBalance)
	}
}

func TestAPIUpdateEntry_Persists(t *testing.T) {
	srv, store := newLedgerTestServer(t)
	defer srv.Close()

	id, err := store.AddEntry(ledger.Entry{
		UseYear: 2026, Date: dateParse(t, "2026-04-01"), Desc: "Original",
		Kind: ledger.KindAllocation, Allotted: 100,
	})
	if err != nil {
		t.Fatal(err)
	}

	resp := putJSON(t, srv.URL+"/api/v1/ledger/entries/"+strconv.FormatInt(id, 10),
		`{"date":"2026-05-15","description":"Updated","kind":"bonus","allotted":75,"tag":"changed"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got apiEntryDTO
	decodeJSON(t, resp, &got)
	if got.Desc != "Updated" || got.Kind != ledger.KindBonus || got.Allotted != 75 || got.Tag != "changed" {
		t.Errorf("got = %+v", got)
	}

	entries, err := store.ListEntries()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Desc != "Updated" {
		t.Fatalf("store entry = %+v, want Desc=Updated", entries[0])
	}
}

func TestAPIUpdateEntry_InvalidDate_400(t *testing.T) {
	srv, store := newLedgerTestServer(t)
	defer srv.Close()

	id, err := store.AddEntry(ledger.Entry{
		UseYear: 2026, Date: dateParse(t, "2026-04-01"), Desc: "Original",
		Kind: ledger.KindAllocation, Allotted: 100,
	})
	if err != nil {
		t.Fatal(err)
	}

	resp := putJSON(t, srv.URL+"/api/v1/ledger/entries/"+strconv.FormatInt(id, 10),
		`{"date":"not-a-date","description":"x","kind":"usage"}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

// TestAPIUpdateEntry_UnknownID_NoRowsAffectedCheck documents a deviation from
// pure REST semantics: ledger.Store.UpdateEntry does not report whether any
// row matched, and adding that plumbing is out of scope for this change, so
// an update against a nonexistent id currently reports success (200) rather
// than 404.
func TestAPIUpdateEntry_UnknownID_NoRowsAffectedCheck(t *testing.T) {
	srv, _ := newLedgerTestServer(t)
	defer srv.Close()

	resp := putJSON(t, srv.URL+"/api/v1/ledger/entries/999999",
		`{"date":"2026-04-01","description":"x","kind":"usage"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (documented deviation, see comment)", resp.StatusCode)
	}
}

func TestAPIDeleteEntry_NoContent(t *testing.T) {
	srv, store := newLedgerTestServer(t)
	defer srv.Close()

	id, err := store.AddEntry(ledger.Entry{
		UseYear: 2026, Date: dateParse(t, "2026-04-01"), Desc: "To delete",
		Kind: ledger.KindAllocation, Allotted: 50,
	})
	if err != nil {
		t.Fatal(err)
	}

	resp := deleteReq(t, srv.URL+"/api/v1/ledger/entries/"+strconv.FormatInt(id, 10))
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}

	entries, err := store.ListEntries()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("store has %d entries, want 0", len(entries))
	}
}

func TestAPIDeleteEntry_BadID_400(t *testing.T) {
	srv, _ := newLedgerTestServer(t)
	defer srv.Close()

	resp := deleteReq(t, srv.URL+"/api/v1/ledger/entries/not-an-id")
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestAPIContracts_AddListDelete(t *testing.T) {
	srv, store := newLedgerTestServer(t)
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/v1/ledger/contracts",
		`{"name":"Point allocation","number":"1234567.000","home_resort":"VGF","annual_points":150,"use_year_month":"Apr"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	var created apiContractDTO
	decodeJSON(t, resp, &created)
	if created.ID == 0 || created.Name != "Point allocation" || created.AnnualPoints != 150 || created.UseYearMonth != "April" {
		t.Errorf("created = %+v", created)
	}

	listResp, err := http.Get(srv.URL + "/api/v1/ledger/contracts")
	if err != nil {
		t.Fatal(err)
	}
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d, want 200", listResp.StatusCode)
	}
	var contracts []apiContractDTO
	decodeJSON(t, listResp, &contracts)
	if len(contracts) != 1 {
		t.Fatalf("contracts = %d, want 1", len(contracts))
	}

	delResp := deleteReq(t, srv.URL+"/api/v1/ledger/contracts/"+strconv.FormatInt(created.ID, 10))
	delResp.Body.Close()
	if delResp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", delResp.StatusCode)
	}

	remaining, err := store.ListContracts()
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 0 {
		t.Fatalf("store has %d contracts, want 0 after delete", len(remaining))
	}
}

func TestAPIAddContract_MissingRequiredFields_400(t *testing.T) {
	srv, _ := newLedgerTestServer(t)
	defer srv.Close()

	cases := []string{
		`{"annual_points":150,"use_year_month":"Apr"}`,            // missing name
		`{"name":"X","use_year_month":"Apr"}`,                     // missing points
		`{"name":"X","annual_points":150}`,                        // missing use_year_month
		`{"name":"X","annual_points":150,"use_year_month":"nah"}`, // bad month
	}
	for _, body := range cases {
		resp := postJSON(t, srv.URL+"/api/v1/ledger/contracts", body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("body %s: status = %d, want 400", body, resp.StatusCode)
		}
		resp.Body.Close()
	}
}

func TestAPISummaries(t *testing.T) {
	srv, store := newLedgerTestServer(t)
	defer srv.Close()

	if _, err := store.AddEntry(ledger.Entry{
		UseYear: 2025, Date: dateParse(t, "2025-04-01"), Desc: "Alloc25",
		Kind: ledger.KindAllocation, Allotted: 100,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddEntry(ledger.Entry{
		UseYear: 2025, Date: dateParse(t, "2025-05-01"), Desc: "Trip25",
		Kind: ledger.KindUsage, Used: 150,
	}); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Get(srv.URL + "/api/v1/ledger/summaries")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got []apiSummaryDTO
	decodeJSON(t, resp, &got)
	if len(got) != 1 {
		t.Fatalf("summaries = %d, want 1", len(got))
	}
	s := got[0]
	if s.UseYear != 2025 || s.Allotted != 100 || s.Used != 150 || s.Net != -50 || !s.OverBorrowed {
		t.Errorf("summary = %+v, want {2025 100 150 -50 true}", s)
	}
}

func TestAPIDistribute_ReturnsCreatedEntries(t *testing.T) {
	srv, store := newLedgerTestServer(t)
	defer srv.Close()

	cid, err := store.AddContract(ledger.Contract{
		Name: "Point allocation", AnnualPoints: 150, UseYearMonth: time.April,
	})
	if err != nil {
		t.Fatal(err)
	}
	lastYear := time.Now().Year()
	if _, err := store.AddEntry(ledger.Entry{
		UseYear: lastYear, Date: time.Date(lastYear, time.April, 1, 0, 0, 0, 0, time.UTC),
		Desc: "Point allocation", Kind: ledger.KindAllocation, Allotted: 150, ContractID: &cid,
	}); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Post(srv.URL+"/api/v1/ledger/distribute", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got []apiEntryDTO
	decodeJSON(t, resp, &got)
	if len(got) != 1 {
		t.Fatalf("distributed = %d entries, want 1", len(got))
	}
	if got[0].UseYear != lastYear+1 || got[0].Allotted != 150 {
		t.Errorf("distributed entry = %+v", got[0])
	}
}

func TestAPI_BearerCorrectSecret_Allowed(t *testing.T) {
	srv, _ := newAuthAPITestServer(t, "s3cret")
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/ledger/entries", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer s3cret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

// TestAPI_BearerWrongSecret_Unauthorized exercises the auth-failure path an
// API client actually gets: an Authorization header present but wrong is
// rejected outright with 401 (no cookie/redirect fallback — see
// authMiddleware in internal/web/auth.go, which this task does not touch).
func TestAPI_BearerWrongSecret_Unauthorized(t *testing.T) {
	srv, _ := newAuthAPITestServer(t, "s3cret")
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/ledger/entries", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer wrong")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}
