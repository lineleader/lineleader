package web

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lineleader/lineleader/internal/ledger"
)

// tripPage GETs /trips/{id} and returns the rendered body, failing the test
// on anything but 200.
func tripPage(t *testing.T, base string, id int64) string {
	t.Helper()
	resp := httpDo(t, http.MethodGet, base+"/trips/"+strconv.FormatInt(id, 10))
	got := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET trip page status = %d, want 200, body:\n%s", resp.StatusCode, got)
	}
	return got
}

// TestTripPage_FundingPreview_FundableRendersSplit proves a trip whose
// unbooked stay can be fully funded renders the funding-preview section
// with the total points and its per-contract split, before Book is ever
// clicked.
func TestTripPage_FundingPreview_FundableRendersSplit(t *testing.T) {
	ts, store := newLedgerTestServer(t)
	defer ts.Close()
	ctx := context.Background()

	cid := addBudgetContract(t, store, 100, time.January)
	fundContract(t, store, cid, 2026, 100)

	tripID, err := store.AddTrip(ctx, ledger.Trip{
		Name:      "Fundable trip",
		StartDate: dateParse(t, "2026-06-01"),
		EndDate:   dateParse(t, "2026-06-10"),
		MinNights: 1,
	})
	if err != nil {
		t.Fatalf("AddTrip: %v", err)
	}
	if _, err := store.AddStay(ctx, ledger.TripStay{
		TripID: tripID, Resort: "Bay Lake Tower", RoomType: "Studio",
		CheckIn: dateParse(t, "2026-06-01"), CheckOut: dateParse(t, "2026-06-05"), Nights: 4, Points: 60,
	}); err != nil {
		t.Fatalf("AddStay: %v", err)
	}

	page := tripPage(t, ts.URL, tripID)
	if !strings.Contains(page, `class="budget-breakdown funding-preview"`) {
		t.Fatalf("expected a funding-preview section, got:\n%s", page)
	}
	if !strings.Contains(page, "60 pts") {
		t.Errorf("expected the total (60 pts) to be drawn to appear, got:\n%s", page)
	}
	if strings.Contains(page, "funding-short") {
		t.Errorf("fundable trip must not render the shortfall message, got:\n%s", page)
	}
}

// TestTripPage_FundingPreview_ShortfallRendersMessage proves a trip that
// can't be fully funded renders the shortfall amount and names the failing
// stay, rather than silently showing nothing (or letting the user find out
// only after clicking Book).
func TestTripPage_FundingPreview_ShortfallRendersMessage(t *testing.T) {
	ts, store := newLedgerTestServer(t)
	defer ts.Close()
	ctx := context.Background()

	cid := addBudgetContract(t, store, 100, time.January)
	fundContract(t, store, cid, 2026, 20) // stay needs 60, only 20 posted

	tripID, err := store.AddTrip(ctx, ledger.Trip{
		Name:      "Short trip",
		StartDate: dateParse(t, "2026-06-01"),
		EndDate:   dateParse(t, "2026-06-10"),
		MinNights: 1,
	})
	if err != nil {
		t.Fatalf("AddTrip: %v", err)
	}
	if _, err := store.AddStay(ctx, ledger.TripStay{
		TripID: tripID, Resort: "Animal Kingdom Villas", RoomType: "1 Bedroom",
		CheckIn: dateParse(t, "2026-06-01"), CheckOut: dateParse(t, "2026-06-05"), Nights: 4, Points: 60,
	}); err != nil {
		t.Fatalf("AddStay: %v", err)
	}

	page := tripPage(t, ts.URL, tripID)
	if !strings.Contains(page, "funding-short") {
		t.Fatalf("expected the shortfall message to render, got:\n%s", page)
	}
	if !strings.Contains(page, "40") {
		t.Errorf("expected the 40-point shortfall to appear, got:\n%s", page)
	}
	if !strings.Contains(page, "Animal Kingdom Villas") {
		t.Errorf("expected the failing stay's resort to be named, got:\n%s", page)
	}
}

// TestTripPage_FundingPreview_HiddenWhenFullyBooked proves the section
// disappears once every stay is actually booked — there's nothing left to
// preview.
func TestTripPage_FundingPreview_HiddenWhenFullyBooked(t *testing.T) {
	ts, store := newLedgerTestServer(t)
	defer ts.Close()
	ctx := context.Background()

	cid := addBudgetContract(t, store, 100, time.January)
	fundContract(t, store, cid, 2026, 100)

	tripID, err := store.AddTrip(ctx, ledger.Trip{
		Name:      "Booked trip",
		StartDate: dateParse(t, "2026-06-01"),
		EndDate:   dateParse(t, "2026-06-10"),
		MinNights: 1,
	})
	if err != nil {
		t.Fatalf("AddTrip: %v", err)
	}
	if _, err := store.AddStay(ctx, ledger.TripStay{
		TripID: tripID, Resort: "Bay Lake Tower", RoomType: "Studio",
		CheckIn: dateParse(t, "2026-06-01"), CheckOut: dateParse(t, "2026-06-05"), Nights: 4, Points: 60,
	}); err != nil {
		t.Fatalf("AddStay: %v", err)
	}
	if err := store.BookTrip(ctx, tripID); err != nil {
		t.Fatalf("BookTrip: %v", err)
	}

	page := tripPage(t, ts.URL, tripID)
	if strings.Contains(page, "funding-preview") {
		t.Errorf("fully-booked trip must render no funding preview, got:\n%s", page)
	}
}

// TestTripPage_FundingPreview_HiddenForEmptyTrip proves a trip with no
// stays at all also renders nothing.
func TestTripPage_FundingPreview_HiddenForEmptyTrip(t *testing.T) {
	ts, store := newLedgerTestServer(t)
	defer ts.Close()
	ctx := context.Background()

	tripID, err := store.AddTrip(ctx, ledger.Trip{
		Name:      "Empty trip",
		StartDate: dateParse(t, "2026-06-01"),
		EndDate:   dateParse(t, "2026-06-10"),
		MinNights: 1,
	})
	if err != nil {
		t.Fatalf("AddTrip: %v", err)
	}

	page := tripPage(t, ts.URL, tripID)
	if strings.Contains(page, "funding-preview") {
		t.Errorf("empty trip must render no funding preview, got:\n%s", page)
	}
}

// TestTripPage_PartiallyBookedStay_RendersDistinctly proves a stay whose
// entries were partially deleted on /ledger (leaving some, but not all, of
// a multi-lot booking's entries) renders as "partially booked" rather than
// silently as booked — the state TripStay.Booked() alone can't tell apart
// (see ledger.TripStay.PartiallyBooked).
func TestTripPage_PartiallyBookedStay_RendersDistinctly(t *testing.T) {
	ts, store := newLedgerTestServer(t)
	defer ts.Close()
	ctx := context.Background()

	// Two contracts, split 95/55, so the stay's 150 points book as two
	// separate entries (trip_book_test.go's worked example) — deleting one
	// of them leaves the stay with a linked entry but short of Points.
	c1 := addBudgetContract(t, store, 100, time.January)
	c2 := addBudgetContract(t, store, 100, time.January)
	fundContract(t, store, c1, 2026, 95)
	fundContract(t, store, c2, 2026, 55)

	tripID, err := store.AddTrip(ctx, ledger.Trip{
		Name:      "Partial booking trip",
		StartDate: dateParse(t, "2026-06-01"),
		EndDate:   dateParse(t, "2026-06-10"),
		MinNights: 1,
	})
	if err != nil {
		t.Fatalf("AddTrip: %v", err)
	}
	stayID, err := store.AddStay(ctx, ledger.TripStay{
		TripID: tripID, Resort: "Bay Lake Tower", RoomType: "Studio",
		CheckIn: dateParse(t, "2026-06-01"), CheckOut: dateParse(t, "2026-06-06"), Nights: 5, Points: 150,
	})
	if err != nil {
		t.Fatalf("AddStay: %v", err)
	}
	if err := store.BookTrip(ctx, tripID); err != nil {
		t.Fatalf("BookTrip: %v", err)
	}

	stays, err := store.ListStays(ctx, tripID)
	if err != nil {
		t.Fatalf("ListStays: %v", err)
	}
	var booked ledger.TripStay
	for _, st := range stays {
		if st.ID == stayID {
			booked = st
		}
	}
	if len(booked.EntryIDs) != 2 {
		t.Fatalf("precondition: expected 2 linked entries, got %v", booked.EntryIDs)
	}

	// Simulate deleting one of the stay's entries on /ledger.
	if err := store.DeleteEntry(ctx, booked.EntryIDs[0]); err != nil {
		t.Fatalf("DeleteEntry: %v", err)
	}

	page := tripPage(t, ts.URL, tripID)
	if !strings.Contains(page, "partially-booked") {
		t.Fatalf("expected the stay row to render as partially booked, got:\n%s", page)
	}
	if strings.Contains(page, "✓ booked") {
		t.Errorf("a partially-booked stay must not render as plain \"✓ booked\", got:\n%s", page)
	}
}
