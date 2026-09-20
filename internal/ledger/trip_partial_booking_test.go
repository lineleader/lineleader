package ledger

import (
	"context"
	"testing"

	"github.com/lineleader/lineleader/internal/ledger/dbgen"
)

// TestTripStay_PartiallyBooked is a pure table test (no database) for the
// three states a stay's EntryIDs/EntryPoints combination can represent:
// never booked, fully booked (EntryPoints == Points), and partially booked
// (EntryPoints < Points because one of its linked entries was deleted on
// /ledger — see PartiallyBooked's doc comment).
func TestTripStay_PartiallyBooked(t *testing.T) {
	cases := []struct {
		name        string
		entryIDs    []int64
		entryPoints int
		points      int
		wantBooked  bool
		wantPartial bool
	}{
		{name: "never booked", entryIDs: nil, entryPoints: 0, points: 130, wantBooked: false, wantPartial: false},
		{name: "fully booked", entryIDs: []int64{1}, entryPoints: 130, points: 130, wantBooked: true, wantPartial: false},
		{name: "partially booked", entryIDs: []int64{1}, entryPoints: 55, points: 130, wantBooked: true, wantPartial: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := TripStay{EntryIDs: c.entryIDs, EntryPoints: c.entryPoints, Points: c.points}
			if got := st.Booked(); got != c.wantBooked {
				t.Errorf("Booked() = %v, want %v", got, c.wantBooked)
			}
			if got := st.PartiallyBooked(); got != c.wantPartial {
				t.Errorf("PartiallyBooked() = %v, want %v", got, c.wantPartial)
			}
		})
	}
}

// TestListStays_PopulatesEntryPointsForPartialBooking books a stay normally
// (one linked entry whose Used equals the stay's Points), then deletes that
// entry directly — simulating someone deleting a booked entry on /ledger,
// which trip_stay_entry's ON DELETE CASCADE leaves the stay with no linked
// entries at all, and separately re-links a smaller entry to reproduce the
// case Booked() gets wrong today: a stay with SOME linked entries whose
// Used total is less than Points. ListStays must report that gap via
// EntryPoints, not just EntryIDs.
func TestListStays_PopulatesEntryPointsForPartialBooking(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	tripID, err := s.AddTrip(ctx, Trip{
		Name:      "Partial booking trip",
		StartDate: date(t, "2026-06-01"),
		EndDate:   date(t, "2026-06-10"),
		MinNights: 1,
	})
	if err != nil {
		t.Fatalf("AddTrip: %v", err)
	}

	stay := TripStay{
		TripID:   tripID,
		Resort:   "Bay Lake Tower",
		RoomType: "1 Bedroom",
		CheckIn:  date(t, "2026-06-05"),
		CheckOut: date(t, "2026-06-10"),
		Nights:   5,
		Points:   130,
	}
	stayID, err := s.AddStay(ctx, stay)
	if err != nil {
		t.Fatalf("AddStay: %v", err)
	}

	// Two entries funding the same stay, summing to less than its 130
	// points — as if a 55-point draw's sibling entry was since deleted.
	e1, err := s.AddEntry(ctx, Entry{UseYear: 2026, Date: date(t, "2026-06-05"), Desc: "BLT 1BR", Kind: KindUsage, Used: 55})
	if err != nil {
		t.Fatalf("AddEntry: %v", err)
	}
	if err := s.q.InsertTripStayEntry(ctx, dbgen.InsertTripStayEntryParams{TripStayID: stayID, EntryID: e1}); err != nil {
		t.Fatalf("InsertTripStayEntry: %v", err)
	}

	got, err := s.ListStays(ctx, tripID)
	if err != nil {
		t.Fatalf("ListStays: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	st := got[0]
	if !st.Booked() {
		t.Fatalf("Booked() = false, want true (has a linked entry)")
	}
	if st.EntryPoints != 55 {
		t.Errorf("EntryPoints = %d, want 55", st.EntryPoints)
	}
	if !st.PartiallyBooked() {
		t.Errorf("PartiallyBooked() = false, want true (55 < 130)")
	}
}

// TestListStays_FullyBookedStayIsNotPartial is TestListStays_...Partial's
// counterpart: a stay whose linked entries sum to exactly its Points is
// booked and NOT partial — EntryPoints must track the real sum, not just
// "has at least one entry".
func TestListStays_FullyBookedStayIsNotPartial(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	tripID, err := s.AddTrip(ctx, Trip{
		Name:      "Fully booked trip",
		StartDate: date(t, "2026-06-01"),
		EndDate:   date(t, "2026-06-10"),
		MinNights: 1,
	})
	if err != nil {
		t.Fatalf("AddTrip: %v", err)
	}

	stayID, err := s.AddStay(ctx, TripStay{
		TripID: tripID, Resort: "BLT", RoomType: "Studio",
		CheckIn: date(t, "2026-06-01"), CheckOut: date(t, "2026-06-05"),
		Nights: 4, Points: 120,
	})
	if err != nil {
		t.Fatalf("AddStay: %v", err)
	}

	e1, err := s.AddEntry(ctx, Entry{UseYear: 2026, Date: date(t, "2026-06-01"), Desc: "BLT Studio", Kind: KindUsage, Used: 70})
	if err != nil {
		t.Fatalf("AddEntry: %v", err)
	}
	e2, err := s.AddEntry(ctx, Entry{UseYear: 2026, Date: date(t, "2026-06-01"), Desc: "BLT Studio", Kind: KindUsage, Used: 50})
	if err != nil {
		t.Fatalf("AddEntry: %v", err)
	}
	for _, eid := range []int64{e1, e2} {
		if err := s.q.InsertTripStayEntry(ctx, dbgen.InsertTripStayEntryParams{TripStayID: stayID, EntryID: eid}); err != nil {
			t.Fatalf("InsertTripStayEntry: %v", err)
		}
	}

	got, err := s.ListStays(ctx, tripID)
	if err != nil {
		t.Fatalf("ListStays: %v", err)
	}
	st := got[0]
	if st.EntryPoints != 120 {
		t.Errorf("EntryPoints = %d, want 120", st.EntryPoints)
	}
	if st.PartiallyBooked() {
		t.Errorf("PartiallyBooked() = true, want false (70+50 == 120)")
	}
}
