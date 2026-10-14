package web

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/lineleader/lineleader/internal/ledger"
)

func addUsageEntry(t *testing.T, store *ledger.Store, used int) int64 {
	t.Helper()
	id, err := store.AddEntry(context.Background(), ledger.Entry{UseYear: 2025, Date: dateParse(t, "2025-03-01"), Kind: ledger.KindUsage, Used: used})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestAPIImportTrip_CreatedAndBooked(t *testing.T) {
	srv, store := newLedgerTestServer(t)
	defer srv.Close()
	e1, e2 := addUsageEntry(t, store, 50), addUsageEntry(t, store, 30)

	body := fmt.Sprintf(`{"name":"Past","stays":[{"resort":"R","room_type":"Studio","view":"Std","check_in":"2025-03-01","check_out":"2025-03-05","points":80,"entry_ids":[%d,%d]}]}`, e1, e2)
	resp := postJSON(t, srv.URL+"/api/v1/trips/import", body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	var got struct {
		ID int64 `json:"id"`
	}
	decodeJSON(t, resp, &got)
	trip, err := store.GetTrip(context.Background(), got.ID)
	if err != nil {
		t.Fatal(err)
	}
	if trip.StartDate.Format("2006-01-02") != "2025-03-01" || trip.EndDate.Format("2006-01-02") != "2025-03-05" || trip.MinNights != 1 {
		t.Errorf("defaults wrong: %+v", trip)
	}
	stays, _ := store.ListStays(context.Background(), got.ID)
	if len(stays) != 1 || stays[0].Nights != 4 || !stays[0].Booked() || stays[0].PartiallyBooked() {
		t.Errorf("stays = %+v", stays)
	}
}

func TestAPIImportTrip_BadRequests(t *testing.T) {
	srv, store := newLedgerTestServer(t)
	defer srv.Close()
	e1 := addUsageEntry(t, store, 50)
	stay := func(extra string) string {
		return fmt.Sprintf(`{"name":"X",%s"stays":[{"resort":"R","room_type":"S","view":"V","check_in":"2025-03-01","check_out":"2025-03-05","points":50,"entry_ids":[%d]}]}`, extra, e1)
	}
	cases := map[string]string{
		"bad json":      `{`,
		"bad date":      `{"name":"X","stays":[{"check_in":"nope","check_out":"2025-03-05","points":1,"entry_ids":[1]}]}`,
		"no stays":      `{"name":"X","stays":[]}`,
		"no name":       `{"stays":[]}`,
		"no entry ids":  `{"name":"X","stays":[{"resort":"R","check_in":"2025-03-01","check_out":"2025-03-05","points":50}]}`,
		"unknown entry": `{"name":"X","stays":[{"resort":"R","check_in":"2025-03-01","check_out":"2025-03-05","points":50,"entry_ids":[99999]}]}`,
		"sum mismatch":  `{"name":"X","stays":[{"resort":"R","check_in":"2025-03-01","check_out":"2025-03-05","points":51,"entry_ids":[` + fmt.Sprint(e1) + `]}]}`,
		"bad range":     stay(`"start_date":"2025-04-01","end_date":"2025-03-01",`),
	}
	for name, body := range cases {
		resp := postJSON(t, srv.URL+"/api/v1/trips/import", body)
		var e apiErrorDTO
		decodeJSON(t, resp, &e)
		if resp.StatusCode != http.StatusBadRequest || e.Error == "" {
			t.Errorf("%s: status = %d err=%q, want 400 with message", name, resp.StatusCode, e.Error)
		}
	}
	if trips, _ := store.ListTrips(context.Background()); len(trips) != 0 {
		t.Errorf("trips created despite errors: %d", len(trips))
	}
}
