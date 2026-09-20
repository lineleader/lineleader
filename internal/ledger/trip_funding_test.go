package ledger

import (
	"context"
	"errors"
	"testing"
	"time"
)

// entryByID returns the entry with the given id, failing the test if it
// isn't present.
func entryByID(t *testing.T, entries []Entry, id int64) Entry {
	t.Helper()
	for _, e := range entries {
		if e.ID == id {
			return e
		}
	}
	t.Fatalf("entryByID(%d): not present in %+v", id, entries)
	return Entry{}
}

// TestPreviewTripFundingMatchesBookTrip is the main risk this issue calls
// out: a preview that disagrees with what BookTrip actually writes is
// worse than no preview at all. Two stays share two contracts' current-year
// lots, and the second stay's need spills across the boundary the FIRST
// stay's draw already ate into — which only comes out right if
// PreviewTripFunding threads `consumed` between stays the same way
// bookTripOnce does. This proves it two ways: the preview predicts a split
// draw for stay2, and BookTrip is then called for real and produces exactly
// those entries.
func TestPreviewTripFundingMatchesBookTrip(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	c1 := addContract(t, s, time.January)
	c2 := addContract(t, s, time.January)
	mustAddAlloc(t, s, c1, 2026, 100)
	mustAddAlloc(t, s, c2, 2026, 100)

	tripID, err := s.AddTrip(ctx, Trip{
		Name:      "Preview matches booking",
		StartDate: date(t, "2026-06-01"),
		EndDate:   date(t, "2026-06-30"),
		MinNights: 1,
	})
	if err != nil {
		t.Fatalf("AddTrip: %v", err)
	}

	stay1ID, err := s.AddStay(ctx, TripStay{
		TripID: tripID, Resort: "A", RoomType: "Studio",
		CheckIn: date(t, "2026-06-01"), CheckOut: date(t, "2026-06-05"), Nights: 4, Points: 60,
	})
	if err != nil {
		t.Fatalf("AddStay stay1: %v", err)
	}
	stay2ID, err := s.AddStay(ctx, TripStay{
		TripID: tripID, Resort: "B", RoomType: "Studio",
		CheckIn: date(t, "2026-06-10"), CheckOut: date(t, "2026-06-15"), Nights: 5, Points: 60,
	})
	if err != nil {
		t.Fatalf("AddStay stay2: %v", err)
	}

	stays, err := s.ListStays(ctx, tripID)
	if err != nil {
		t.Fatalf("ListStays: %v", err)
	}

	preview, err := s.PreviewTripFunding(ctx, stays)
	if err != nil {
		t.Fatalf("PreviewTripFunding: %v", err)
	}
	if !preview.Fundable {
		t.Fatalf("Fundable = false, want true: %+v", preview)
	}
	if len(preview.Stays) != 2 {
		t.Fatalf("len(preview.Stays) = %d, want 2: %+v", len(preview.Stays), preview.Stays)
	}

	s1 := preview.Stays[0]
	if s1.StayID != stay1ID || !s1.Fundable {
		t.Fatalf("preview.Stays[0] = %+v, want fundable stay %d", s1, stay1ID)
	}
	wantS1 := []PointDraw{{ContractID: c1, UseYear: 2026, Disposition: DispositionCurrent, Points: 60}}
	if len(s1.Draws) != len(wantS1) || s1.Draws[0] != wantS1[0] {
		t.Errorf("preview stay1 draws = %+v, want %+v", s1.Draws, wantS1)
	}

	s2 := preview.Stays[1]
	if s2.StayID != stay2ID || !s2.Fundable {
		t.Fatalf("preview.Stays[1] = %+v, want fundable stay %d", s2, stay2ID)
	}
	wantS2 := []PointDraw{
		{ContractID: c1, UseYear: 2026, Disposition: DispositionCurrent, Points: 40},
		{ContractID: c2, UseYear: 2026, Disposition: DispositionCurrent, Points: 20},
	}
	if len(s2.Draws) != len(wantS2) {
		t.Fatalf("preview stay2 draws = %+v, want %+v", s2.Draws, wantS2)
	}
	for i := range wantS2 {
		if s2.Draws[i] != wantS2[i] {
			t.Errorf("preview stay2 draw[%d] = %+v, want %+v", i, s2.Draws[i], wantS2[i])
		}
	}

	// Now actually book it, and check the preview predicted exactly what
	// got written.
	if err := s.BookTrip(ctx, tripID); err != nil {
		t.Fatalf("BookTrip: %v", err)
	}
	entries, err := s.ListEntries(ctx)
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	staysAfter, err := s.ListStays(ctx, tripID)
	if err != nil {
		t.Fatalf("ListStays after booking: %v", err)
	}

	for _, sf := range preview.Stays {
		st := findStay(t, staysAfter, sf.StayID)
		if len(st.EntryIDs) != len(sf.Draws) {
			t.Fatalf("stay %d has %d linked entries, want %d matching the preview's draws", sf.StayID, len(st.EntryIDs), len(sf.Draws))
		}
		for i, draw := range sf.Draws {
			e := entryByID(t, entries, st.EntryIDs[i])
			if e.ContractID == nil || *e.ContractID != draw.ContractID {
				t.Errorf("stay %d entry[%d].ContractID = %v, want %d", sf.StayID, i, e.ContractID, draw.ContractID)
			}
			if e.UseYear != draw.UseYear {
				t.Errorf("stay %d entry[%d].UseYear = %d, want %d", sf.StayID, i, e.UseYear, draw.UseYear)
			}
			if e.Used != draw.Points {
				t.Errorf("stay %d entry[%d].Used = %d, want %d", sf.StayID, i, e.Used, draw.Points)
			}
			if want := DispositionTag(draw.Disposition); e.Tag != want {
				t.Errorf("stay %d entry[%d].Tag = %q, want %q", sf.StayID, i, e.Tag, want)
			}
		}
	}
}

// TestPreviewTripFundingReportsShortfallAndStopsAtFirstFailure proves the
// preview identifies which stay first can't be funded, how many points it
// is short, and never evaluates stays after that one — the same
// all-or-nothing point BookTrip's own doc comment makes: stays after a
// failure are never even attempted, so a preview showing them as fundable
// would be misleading.
func TestPreviewTripFundingReportsShortfallAndStopsAtFirstFailure(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	c1 := addContract(t, s, time.January)
	mustAddAlloc(t, s, c1, 2026, 90) // covers stay1 (60), leaving 30

	tripID, err := s.AddTrip(ctx, Trip{
		Name:      "Short trip",
		StartDate: date(t, "2026-06-01"),
		EndDate:   date(t, "2026-06-30"),
		MinNights: 1,
	})
	if err != nil {
		t.Fatalf("AddTrip: %v", err)
	}

	stay1ID, err := s.AddStay(ctx, TripStay{
		TripID: tripID, Resort: "A", RoomType: "Studio",
		CheckIn: date(t, "2026-06-01"), CheckOut: date(t, "2026-06-05"), Nights: 4, Points: 60,
	})
	if err != nil {
		t.Fatalf("AddStay stay1: %v", err)
	}
	stay2ID, err := s.AddStay(ctx, TripStay{
		TripID: tripID, Resort: "B", RoomType: "Studio",
		CheckIn: date(t, "2026-06-10"), CheckOut: date(t, "2026-06-15"), Nights: 5, Points: 50, // only 30 left → short by 20
	})
	if err != nil {
		t.Fatalf("AddStay stay2: %v", err)
	}
	if _, err := s.AddStay(ctx, TripStay{
		TripID: tripID, Resort: "C", RoomType: "Studio",
		CheckIn: date(t, "2026-06-20"), CheckOut: date(t, "2026-06-25"), Nights: 5, Points: 10,
	}); err != nil {
		t.Fatalf("AddStay stay3: %v", err)
	}

	stays, err := s.ListStays(ctx, tripID)
	if err != nil {
		t.Fatalf("ListStays: %v", err)
	}

	preview, err := s.PreviewTripFunding(ctx, stays)
	if err != nil {
		t.Fatalf("PreviewTripFunding: %v", err)
	}
	if preview.Fundable {
		t.Fatalf("Fundable = true, want false: %+v", preview)
	}
	if len(preview.Stays) != 2 {
		t.Fatalf("len(preview.Stays) = %d, want 2 (stops at the first failing stay, never reaching stay3)", len(preview.Stays))
	}
	if got := preview.Stays[0]; got.StayID != stay1ID || !got.Fundable {
		t.Errorf("preview.Stays[0] = %+v, want fundable stay %d", got, stay1ID)
	}
	got2 := preview.Stays[1]
	if got2.StayID != stay2ID {
		t.Fatalf("preview.Stays[1].StayID = %d, want %d", got2.StayID, stay2ID)
	}
	if got2.Fundable {
		t.Errorf("preview.Stays[1].Fundable = true, want false")
	}
	if got2.ShortBy != 20 {
		t.Errorf("preview.Stays[1].ShortBy = %d, want 20", got2.ShortBy)
	}

	// BookTrip must fail for the same reason, and roll back completely.
	if err := s.BookTrip(ctx, tripID); !errors.Is(err, ErrInsufficientPoints) {
		t.Fatalf("BookTrip err = %v, want ErrInsufficientPoints", err)
	}
	staysAfter, err := s.ListStays(ctx, tripID)
	if err != nil {
		t.Fatalf("ListStays after failed BookTrip: %v", err)
	}
	for _, st := range staysAfter {
		if st.Booked() {
			t.Errorf("stay %d is booked (%v), want none booked after a rolled-back BookTrip", st.ID, st.EntryIDs)
		}
	}
}

// TestPreviewTripFundingShortfallWhenLotsAreIneligible exercises shortfall's
// binary search directly, rather than the plain aggregate fast-fail path:
// a contract has 200 real, posted points, but all of them sit in a use
// year outside the banked/current/borrowed window for this stay's check-in
// date, so AllocateStayPoints itself (not the aggregate pre-check) is what
// fails, with zero points actually drawable.
func TestPreviewTripFundingShortfallWhenLotsAreIneligible(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	c1 := addContract(t, s, time.April)
	mustAddAlloc(t, s, c1, 2028, 200) // 2028 is neither banked, current nor borrowed for a UY2026 check-in

	tripID, err := s.AddTrip(ctx, Trip{
		Name:      "Ineligible lots",
		StartDate: date(t, "2026-05-01"),
		EndDate:   date(t, "2026-05-10"),
		MinNights: 1,
	})
	if err != nil {
		t.Fatalf("AddTrip: %v", err)
	}
	stayID, err := s.AddStay(ctx, TripStay{
		TripID: tripID, Resort: "A", RoomType: "Studio",
		CheckIn: date(t, "2026-05-01"), CheckOut: date(t, "2026-05-05"), Nights: 4, Points: 50,
	})
	if err != nil {
		t.Fatalf("AddStay: %v", err)
	}

	stays, err := s.ListStays(ctx, tripID)
	if err != nil {
		t.Fatalf("ListStays: %v", err)
	}
	preview, err := s.PreviewTripFunding(ctx, stays)
	if err != nil {
		t.Fatalf("PreviewTripFunding: %v", err)
	}
	if preview.Fundable {
		t.Fatalf("Fundable = true, want false (the 2028 lot is out of window)")
	}
	if len(preview.Stays) != 1 || preview.Stays[0].StayID != stayID {
		t.Fatalf("preview.Stays = %+v, want exactly one entry for stay %d", preview.Stays, stayID)
	}
	if got := preview.Stays[0].ShortBy; got != 50 {
		t.Errorf("ShortBy = %d, want 50 (no eligible candidates at all)", got)
	}
}

// TestPreviewTripFundingSkipsAlreadyBookedStays proves PreviewTripFunding
// mirrors bookTripOnce's own loop, which skips any stay with Booked() ==
// true (re-booking must be a no-op) — passing the full ListStays result,
// booked stays included, must not consume points on their behalf a second
// time.
func TestPreviewTripFundingSkipsAlreadyBookedStays(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	c1 := addContract(t, s, time.January)
	mustAddAlloc(t, s, c1, 2026, 60)

	tripID, err := s.AddTrip(ctx, Trip{
		Name:      "Mixed booking state",
		StartDate: date(t, "2026-06-01"),
		EndDate:   date(t, "2026-06-20"),
		MinNights: 1,
	})
	if err != nil {
		t.Fatalf("AddTrip: %v", err)
	}
	stay1ID, err := s.AddStay(ctx, TripStay{
		TripID: tripID, Resort: "A", RoomType: "Studio",
		CheckIn: date(t, "2026-06-01"), CheckOut: date(t, "2026-06-05"), Nights: 4, Points: 60,
	})
	if err != nil {
		t.Fatalf("AddStay stay1: %v", err)
	}

	// Book stay1 alone first, spending all 60 points, before stay2 (which
	// needs points that won't be there) even exists.
	if err := s.BookTrip(ctx, tripID); err != nil {
		t.Fatalf("BookTrip (stay1 only): %v", err)
	}

	if _, err := s.AddStay(ctx, TripStay{
		TripID: tripID, Resort: "B", RoomType: "Studio",
		CheckIn: date(t, "2026-06-10"), CheckOut: date(t, "2026-06-15"), Nights: 5, Points: 10,
	}); err != nil {
		t.Fatalf("AddStay stay2: %v", err)
	}

	stays, err := s.ListStays(ctx, tripID)
	if err != nil {
		t.Fatalf("ListStays: %v", err)
	}
	preview, err := s.PreviewTripFunding(ctx, stays)
	if err != nil {
		t.Fatalf("PreviewTripFunding: %v", err)
	}
	// stay1 is booked and must be skipped entirely; stay2 needs 10 points
	// but none are left (all 60 went to stay1), so the only entry in the
	// preview is stay2's failure.
	if preview.Fundable {
		t.Fatalf("Fundable = true, want false (no points left for stay2)")
	}
	if len(preview.Stays) != 1 {
		t.Fatalf("len(preview.Stays) = %d, want 1 (booked stay1 skipped)", len(preview.Stays))
	}
	if preview.Stays[0].StayID == stay1ID {
		t.Errorf("preview evaluated already-booked stay1, want it skipped")
	}
}
