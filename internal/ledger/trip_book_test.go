package ledger

import (
	"context"
	"testing"
	"time"
)

// findEntryByDesc returns the entry whose Desc matches want, failing the
// test if there isn't exactly one. Several tests below need to pull a
// specific booked-stay entry out of ListEntries by the description BookTrip
// composed for it, since IDs aren't known ahead of the call.
func findEntryByDesc(t *testing.T, entries []Entry, want string) Entry {
	t.Helper()
	var found []Entry
	for _, e := range entries {
		if e.Desc == want {
			found = append(found, e)
		}
	}
	if len(found) != 1 {
		t.Fatalf("findEntryByDesc(%q): found %d matches in %+v, want exactly 1", want, len(found), entries)
	}
	return found[0]
}

// findStay returns the stay with the given id, failing the test if it isn't
// present in stays.
func findStay(t *testing.T, stays []TripStay, id int64) TripStay {
	t.Helper()
	for _, st := range stays {
		if st.ID == id {
			return st
		}
	}
	t.Fatalf("findStay(%d): not present in %+v", id, stays)
	return TripStay{}
}

// TestBookTrip_WritesOneEntryPerStay is the basic happy path: two unbooked
// stays on a trip, no contracts (so UseYearStartMonth defaults to January),
// book them both and check every field BookTrip is responsible for shaping
// — not just that an entry got created.
func TestBookTrip_WritesOneEntryPerStay(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	tripID, err := s.AddTrip(ctx, Trip{
		Name:      "Test Trip Book",
		StartDate: date(t, "2026-06-01"),
		EndDate:   date(t, "2026-06-10"),
		MinNights: 1,
	})
	if err != nil {
		t.Fatalf("AddTrip: %v", err)
	}

	stay1 := TripStay{
		TripID:   tripID,
		Resort:   "Bay Lake Tower",
		RoomType: "Studio",
		CheckIn:  date(t, "2026-06-01"),
		CheckOut: date(t, "2026-06-05"),
		Nights:   4,
		Points:   120,
	}
	stay2 := TripStay{
		TripID:   tripID,
		Resort:   "Animal Kingdom Villas",
		RoomType: "1 Bedroom",
		CheckIn:  date(t, "2026-06-05"),
		CheckOut: date(t, "2026-06-10"),
		Nights:   5,
		Points:   160,
	}
	stay1ID, err := s.AddStay(ctx, stay1)
	if err != nil {
		t.Fatalf("AddStay stay1: %v", err)
	}
	stay2ID, err := s.AddStay(ctx, stay2)
	if err != nil {
		t.Fatalf("AddStay stay2: %v", err)
	}

	if err := s.BookTrip(ctx, tripID); err != nil {
		t.Fatalf("BookTrip: %v", err)
	}

	entries, err := s.ListEntries(ctx)
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2: %+v", len(entries), entries)
	}

	e1 := findEntryByDesc(t, entries, "Test Trip Book — Bay Lake Tower Studio")
	if e1.Kind != KindUsage {
		t.Errorf("e1.Kind = %q, want %q", e1.Kind, KindUsage)
	}
	if e1.Used != 120 {
		t.Errorf("e1.Used = %d, want 120", e1.Used)
	}
	if e1.Allotted != 0 {
		t.Errorf("e1.Allotted = %d, want 0", e1.Allotted)
	}
	if !e1.Date.Equal(stay1.CheckIn) {
		t.Errorf("e1.Date = %v, want %v", e1.Date, stay1.CheckIn)
	}
	if e1.UseYear != 2026 {
		t.Errorf("e1.UseYear = %d, want 2026 (no contracts => January use-year start)", e1.UseYear)
	}
	if e1.Tag != "" {
		t.Errorf("e1.Tag = %q, want empty", e1.Tag)
	}
	if e1.ContractID != nil {
		t.Errorf("e1.ContractID = %v, want nil", e1.ContractID)
	}

	e2 := findEntryByDesc(t, entries, "Test Trip Book — Animal Kingdom Villas 1 Bedroom")
	if e2.Used != 160 {
		t.Errorf("e2.Used = %d, want 160", e2.Used)
	}
	if e2.UseYear != 2026 {
		t.Errorf("e2.UseYear = %d, want 2026", e2.UseYear)
	}

	stays, err := s.ListStays(ctx, tripID)
	if err != nil {
		t.Fatalf("ListStays: %v", err)
	}
	got1 := findStay(t, stays, stay1ID)
	if got1.EntryID == nil || *got1.EntryID != e1.ID {
		t.Errorf("stay1.EntryID = %v, want *%d", got1.EntryID, e1.ID)
	}
	got2 := findStay(t, stays, stay2ID)
	if got2.EntryID == nil || *got2.EntryID != e2.ID {
		t.Errorf("stay2.EntryID = %v, want *%d", got2.EntryID, e2.ID)
	}
}

// TestBookTrip_PerStayUseYearAcrossBoundary is the rule that distinguishes
// per-stay from per-trip attribution: each entry's UseYear is computed from
// its OWN stay's check-in date, not from the trip's start date, because a
// trip window can straddle a use-year boundary even though the displayed
// budget cannot. With an April use-year start, a 2026-03-20 check-in is use
// year 2025 and a 2026-04-05 check-in — five days later, same trip — is use
// year 2026. Getting this wrong (e.g. stamping every entry with the trip's
// own use year) would silently misattribute one stay's points to the wrong
// year's balance.
func TestBookTrip_PerStayUseYearAcrossBoundary(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	if _, err := s.AddContract(ctx, Contract{
		Name:         "Point allocation",
		AnnualPoints: 150,
		UseYearMonth: time.April,
	}); err != nil {
		t.Fatalf("AddContract: %v", err)
	}

	tripID, err := s.AddTrip(ctx, Trip{
		Name:      "Boundary Trip",
		StartDate: date(t, "2026-03-20"),
		EndDate:   date(t, "2026-04-10"),
		MinNights: 1,
	})
	if err != nil {
		t.Fatalf("AddTrip: %v", err)
	}

	beforeBoundary := TripStay{
		TripID:   tripID,
		Resort:   "Riviera",
		RoomType: "Studio",
		CheckIn:  date(t, "2026-03-20"),
		CheckOut: date(t, "2026-03-24"),
		Nights:   4,
		Points:   80,
	}
	afterBoundary := TripStay{
		TripID:   tripID,
		Resort:   "Riviera",
		RoomType: "1 Bedroom",
		CheckIn:  date(t, "2026-04-05"),
		CheckOut: date(t, "2026-04-10"),
		Nights:   5,
		Points:   140,
	}
	if _, err := s.AddStay(ctx, beforeBoundary); err != nil {
		t.Fatalf("AddStay beforeBoundary: %v", err)
	}
	if _, err := s.AddStay(ctx, afterBoundary); err != nil {
		t.Fatalf("AddStay afterBoundary: %v", err)
	}

	if err := s.BookTrip(ctx, tripID); err != nil {
		t.Fatalf("BookTrip: %v", err)
	}

	entries, err := s.ListEntries(ctx)
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2: %+v", len(entries), entries)
	}

	before := findEntryByDesc(t, entries, "Boundary Trip — Riviera Studio")
	if before.UseYear != 2025 {
		t.Errorf("before-boundary entry UseYear = %d, want 2025 (check-in 2026-03-20 precedes the April start)", before.UseYear)
	}

	after := findEntryByDesc(t, entries, "Boundary Trip — Riviera 1 Bedroom")
	if after.UseYear != 2026 {
		t.Errorf("after-boundary entry UseYear = %d, want 2026 (check-in 2026-04-05 is on/after the April start)", after.UseYear)
	}
}

// TestBookTrip_IdempotentReBook confirms that calling BookTrip a second
// time on an already-booked trip is a no-op: it must not create duplicate
// entries or rewrite the existing entry_id links. This is what makes a
// double-submitted "Book this trip" form safe.
func TestBookTrip_IdempotentReBook(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	tripID, err := s.AddTrip(ctx, Trip{
		Name:      "Rebook Trip",
		StartDate: date(t, "2026-06-01"),
		EndDate:   date(t, "2026-06-10"),
		MinNights: 1,
	})
	if err != nil {
		t.Fatalf("AddTrip: %v", err)
	}
	stay1ID, err := s.AddStay(ctx, TripStay{
		TripID: tripID, Resort: "BLT", RoomType: "Studio",
		CheckIn: date(t, "2026-06-01"), CheckOut: date(t, "2026-06-05"),
		Nights: 4, Points: 100,
	})
	if err != nil {
		t.Fatalf("AddStay: %v", err)
	}
	stay2ID, err := s.AddStay(ctx, TripStay{
		TripID: tripID, Resort: "AKV", RoomType: "1BR",
		CheckIn: date(t, "2026-06-05"), CheckOut: date(t, "2026-06-10"),
		Nights: 5, Points: 150,
	})
	if err != nil {
		t.Fatalf("AddStay: %v", err)
	}

	if err := s.BookTrip(ctx, tripID); err != nil {
		t.Fatalf("first BookTrip: %v", err)
	}

	entriesBefore, err := s.ListEntries(ctx)
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	staysBefore, err := s.ListStays(ctx, tripID)
	if err != nil {
		t.Fatalf("ListStays: %v", err)
	}
	entryIDBefore1 := findStay(t, staysBefore, stay1ID).EntryID
	entryIDBefore2 := findStay(t, staysBefore, stay2ID).EntryID
	if entryIDBefore1 == nil || entryIDBefore2 == nil {
		t.Fatalf("expected both stays booked after first BookTrip: %+v", staysBefore)
	}

	if err := s.BookTrip(ctx, tripID); err != nil {
		t.Fatalf("second BookTrip: %v", err)
	}

	entriesAfter, err := s.ListEntries(ctx)
	if err != nil {
		t.Fatalf("ListEntries after second BookTrip: %v", err)
	}
	if len(entriesAfter) != len(entriesBefore) {
		t.Fatalf("entry count changed on re-book: before %d, after %d", len(entriesBefore), len(entriesAfter))
	}

	staysAfter, err := s.ListStays(ctx, tripID)
	if err != nil {
		t.Fatalf("ListStays after second BookTrip: %v", err)
	}
	entryIDAfter1 := findStay(t, staysAfter, stay1ID).EntryID
	entryIDAfter2 := findStay(t, staysAfter, stay2ID).EntryID
	if entryIDAfter1 == nil || *entryIDAfter1 != *entryIDBefore1 {
		t.Errorf("stay1 EntryID changed on re-book: before %v, after %v", entryIDBefore1, entryIDAfter1)
	}
	if entryIDAfter2 == nil || *entryIDAfter2 != *entryIDBefore2 {
		t.Errorf("stay2 EntryID changed on re-book: before %v, after %v", entryIDBefore2, entryIDAfter2)
	}
}

// TestBookTrip_RollbackIsAllOrNothing is the atomicity proof: BookTrip must
// run every stay's AddEntry inside one transaction, not one transaction per
// stay. We install a temporary CHECK constraint that only the SECOND
// stay's computed description violates (stays are processed in ListStays'
// (check_in, id) order, which is what makes "second" deterministic here).
// The first stay's insert succeeds inside the doomed transaction, then the
// second fails the CHECK — if BookTrip commits per-stay instead of once for
// the whole trip, the first stay's entry would survive; it must not.
func TestBookTrip_RollbackIsAllOrNothing(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	if _, err := s.db.ExecContext(ctx,
		`ALTER TABLE entry ADD CONSTRAINT tmp_boom CHECK (description NOT LIKE '%BOOM%')`,
	); err != nil {
		t.Fatalf("installing tmp_boom CHECK: %v", err)
	}

	tripID, err := s.AddTrip(ctx, Trip{
		Name:      "Rollback Trip",
		StartDate: date(t, "2026-06-01"),
		EndDate:   date(t, "2026-06-10"),
		MinNights: 1,
	})
	if err != nil {
		t.Fatalf("AddTrip: %v", err)
	}
	// First by (check_in, id): check-in earlier, clean description.
	firstStayID, err := s.AddStay(ctx, TripStay{
		TripID: tripID, Resort: "BLT", RoomType: "Studio",
		CheckIn: date(t, "2026-06-01"), CheckOut: date(t, "2026-06-05"),
		Nights: 4, Points: 100,
	})
	if err != nil {
		t.Fatalf("AddStay firstStay: %v", err)
	}
	// Second by (check_in, id): later check-in, resort name that makes the
	// composed Desc trip the CHECK constraint.
	if _, err := s.AddStay(ctx, TripStay{
		TripID: tripID, Resort: "BOOM Resort", RoomType: "1BR",
		CheckIn: date(t, "2026-06-05"), CheckOut: date(t, "2026-06-10"),
		Nights: 5, Points: 150,
	}); err != nil {
		t.Fatalf("AddStay secondStay: %v", err)
	}

	if err := s.BookTrip(ctx, tripID); err == nil {
		t.Fatal("BookTrip: got nil error, want the CHECK violation from the second stay's insert")
	}

	entries, err := s.ListEntries(ctx)
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("ListEntries after failed BookTrip = %+v, want empty — the first stay's insert must have rolled back too", entries)
	}

	stays, err := s.ListStays(ctx, tripID)
	if err != nil {
		t.Fatalf("ListStays: %v", err)
	}
	first := findStay(t, stays, firstStayID)
	if first.EntryID != nil {
		t.Errorf("first stay EntryID = %v, want nil — its insert must not have survived the rolled-back transaction", first.EntryID)
	}
}

// TestUnbookTrip_RemovesLinkedEntriesOnly seeds one unrelated, hand-entered
// usage entry (not attached to any stay) alongside a booked trip, then
// unbooks the trip. Only the two entries BookTrip created for this trip's
// stays should disappear; the unrelated entry — and the stays themselves —
// must survive.
func TestUnbookTrip_RemovesLinkedEntriesOnly(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	unrelatedID, err := s.AddEntry(ctx, Entry{
		UseYear: 2026,
		Date:    date(t, "2026-01-15"),
		Desc:    "Hand-entered unrelated usage",
		Kind:    KindUsage,
		Used:    50,
	})
	if err != nil {
		t.Fatalf("AddEntry (unrelated): %v", err)
	}

	tripID, err := s.AddTrip(ctx, Trip{
		Name:      "Unbook Trip",
		StartDate: date(t, "2026-06-01"),
		EndDate:   date(t, "2026-06-10"),
		MinNights: 1,
	})
	if err != nil {
		t.Fatalf("AddTrip: %v", err)
	}
	stay1ID, err := s.AddStay(ctx, TripStay{
		TripID: tripID, Resort: "BLT", RoomType: "Studio",
		CheckIn: date(t, "2026-06-01"), CheckOut: date(t, "2026-06-05"),
		Nights: 4, Points: 100,
	})
	if err != nil {
		t.Fatalf("AddStay: %v", err)
	}
	stay2ID, err := s.AddStay(ctx, TripStay{
		TripID: tripID, Resort: "AKV", RoomType: "1BR",
		CheckIn: date(t, "2026-06-05"), CheckOut: date(t, "2026-06-10"),
		Nights: 5, Points: 150,
	})
	if err != nil {
		t.Fatalf("AddStay: %v", err)
	}
	if err := s.BookTrip(ctx, tripID); err != nil {
		t.Fatalf("BookTrip: %v", err)
	}

	if err := s.UnbookTrip(ctx, tripID); err != nil {
		t.Fatalf("UnbookTrip: %v", err)
	}

	entries, err := s.ListEntries(ctx)
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	found := false
	for _, e := range entries {
		if e.ID == unrelatedID {
			found = true
		}
		if e.Desc != "Hand-entered unrelated usage" && e.ID != unrelatedID {
			// any remaining entry must be the unrelated one; anything else
			// means a booked-trip entry survived unbooking.
			t.Errorf("unexpected surviving entry after UnbookTrip: %+v", e)
		}
	}
	if !found {
		t.Error("unrelated entry did not survive UnbookTrip")
	}
	if len(entries) != 1 {
		t.Errorf("ListEntries after UnbookTrip = %+v, want exactly the unrelated entry", entries)
	}

	stays, err := s.ListStays(ctx, tripID)
	if err != nil {
		t.Fatalf("ListStays: %v", err)
	}
	if got := findStay(t, stays, stay1ID); got.EntryID != nil {
		t.Errorf("stay1.EntryID = %v, want nil after UnbookTrip", got.EntryID)
	}
	if got := findStay(t, stays, stay2ID); got.EntryID != nil {
		t.Errorf("stay2.EntryID = %v, want nil after UnbookTrip", got.EntryID)
	}
}

// TestDeleteTrip_LeavesNoOrphanEntries is the sharpest trap in this
// feature. DeleteTrip must delete the trip's ledger entries BEFORE deleting
// the trip row, because trip_stay cascades away (ON DELETE CASCADE) the
// instant the trip is deleted — and once trip_stay is gone, so is the only
// link (trip_stay.entry_id) that could find those entries again. Deleting
// the trip first would strand the usage rows in the ledger forever, with no
// way to identify or reverse them. This test seeds an unrelated entry to
// prove the deletion is scoped correctly, not just that "some" entries
// disappear.
func TestDeleteTrip_LeavesNoOrphanEntries(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	unrelatedID, err := s.AddEntry(ctx, Entry{
		UseYear: 2026,
		Date:    date(t, "2026-01-15"),
		Desc:    "Hand-entered unrelated usage",
		Kind:    KindUsage,
		Used:    50,
	})
	if err != nil {
		t.Fatalf("AddEntry (unrelated): %v", err)
	}

	tripID, err := s.AddTrip(ctx, Trip{
		Name:      "Delete Trip",
		StartDate: date(t, "2026-06-01"),
		EndDate:   date(t, "2026-06-10"),
		MinNights: 1,
	})
	if err != nil {
		t.Fatalf("AddTrip: %v", err)
	}
	if _, err := s.AddStay(ctx, TripStay{
		TripID: tripID, Resort: "BLT", RoomType: "Studio",
		CheckIn: date(t, "2026-06-01"), CheckOut: date(t, "2026-06-05"),
		Nights: 4, Points: 100,
	}); err != nil {
		t.Fatalf("AddStay: %v", err)
	}
	if _, err := s.AddStay(ctx, TripStay{
		TripID: tripID, Resort: "AKV", RoomType: "1BR",
		CheckIn: date(t, "2026-06-05"), CheckOut: date(t, "2026-06-10"),
		Nights: 5, Points: 150,
	}); err != nil {
		t.Fatalf("AddStay: %v", err)
	}
	if err := s.BookTrip(ctx, tripID); err != nil {
		t.Fatalf("BookTrip: %v", err)
	}

	entriesBefore, err := s.ListEntries(ctx)
	if err != nil {
		t.Fatalf("ListEntries before delete: %v", err)
	}
	var tripEntryIDs []int64
	for _, e := range entriesBefore {
		if e.ID != unrelatedID {
			tripEntryIDs = append(tripEntryIDs, e.ID)
		}
	}
	if len(tripEntryIDs) != 2 {
		t.Fatalf("expected 2 trip entries before delete, got %d: %+v", len(tripEntryIDs), entriesBefore)
	}

	if err := s.DeleteTrip(ctx, tripID); err != nil {
		t.Fatalf("DeleteTrip: %v", err)
	}

	entriesAfter, err := s.ListEntries(ctx)
	if err != nil {
		t.Fatalf("ListEntries after delete: %v", err)
	}
	byID := make(map[int64]bool, len(entriesAfter))
	for _, e := range entriesAfter {
		byID[e.ID] = true
	}
	for _, id := range tripEntryIDs {
		if byID[id] {
			t.Errorf("trip entry %d still present after DeleteTrip — orphaned", id)
		}
	}
	if !byID[unrelatedID] {
		t.Error("unrelated entry was removed by DeleteTrip; it should survive")
	}

	if _, err := s.GetTrip(ctx, tripID); err == nil {
		t.Error("GetTrip after DeleteTrip: got nil error, want ErrTripNotFound")
	}
}

// TestDeleteStay_RemovesOnlyThatStayAndItsEntry books both stays on a trip,
// deletes one of them, and checks that the deletion is scoped to that
// single stay: the other stay and its own entry must remain intact and
// still linked.
func TestDeleteStay_RemovesOnlyThatStayAndItsEntry(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	tripID, err := s.AddTrip(ctx, Trip{
		Name:      "Stay Delete Trip",
		StartDate: date(t, "2026-06-01"),
		EndDate:   date(t, "2026-06-10"),
		MinNights: 1,
	})
	if err != nil {
		t.Fatalf("AddTrip: %v", err)
	}
	stay1ID, err := s.AddStay(ctx, TripStay{
		TripID: tripID, Resort: "BLT", RoomType: "Studio",
		CheckIn: date(t, "2026-06-01"), CheckOut: date(t, "2026-06-05"),
		Nights: 4, Points: 100,
	})
	if err != nil {
		t.Fatalf("AddStay: %v", err)
	}
	stay2ID, err := s.AddStay(ctx, TripStay{
		TripID: tripID, Resort: "AKV", RoomType: "1BR",
		CheckIn: date(t, "2026-06-05"), CheckOut: date(t, "2026-06-10"),
		Nights: 5, Points: 150,
	})
	if err != nil {
		t.Fatalf("AddStay: %v", err)
	}
	if err := s.BookTrip(ctx, tripID); err != nil {
		t.Fatalf("BookTrip: %v", err)
	}

	staysBefore, err := s.ListStays(ctx, tripID)
	if err != nil {
		t.Fatalf("ListStays before delete: %v", err)
	}
	stay1EntryID := findStay(t, staysBefore, stay1ID).EntryID
	stay2EntryID := findStay(t, staysBefore, stay2ID).EntryID
	if stay1EntryID == nil || stay2EntryID == nil {
		t.Fatalf("expected both stays booked before DeleteStay: %+v", staysBefore)
	}

	if err := s.DeleteStay(ctx, stay1ID); err != nil {
		t.Fatalf("DeleteStay: %v", err)
	}

	staysAfter, err := s.ListStays(ctx, tripID)
	if err != nil {
		t.Fatalf("ListStays after delete: %v", err)
	}
	for _, st := range staysAfter {
		if st.ID == stay1ID {
			t.Errorf("stay1 (%d) still present in ListStays after DeleteStay", stay1ID)
		}
	}
	remaining := findStay(t, staysAfter, stay2ID)
	if remaining.EntryID == nil || *remaining.EntryID != *stay2EntryID {
		t.Errorf("stay2.EntryID after DeleteStay = %v, want unchanged *%d", remaining.EntryID, *stay2EntryID)
	}

	entries, err := s.ListEntries(ctx)
	if err != nil {
		t.Fatalf("ListEntries after delete: %v", err)
	}
	byID := make(map[int64]bool, len(entries))
	for _, e := range entries {
		byID[e.ID] = true
	}
	if byID[*stay1EntryID] {
		t.Errorf("stay1's entry (%d) still present after DeleteStay", *stay1EntryID)
	}
	if !byID[*stay2EntryID] {
		t.Errorf("stay2's entry (%d) missing after DeleteStay; it should be untouched", *stay2EntryID)
	}
}

// TestDeleteEntry_BehindBookedStayClearsTheLink documents that
// trip_stay.entry_id's ON DELETE SET NULL is CORRECT behavior, not a bug:
// deleting a ledger entry directly on /ledger (bypassing UnbookTrip or
// DeleteStay entirely) must not leave a dangling foreign key or break the
// stay. The stay and its trip both keep existing; the stay's booked-ness
// simply reverts to "not booked" because that status is always derived from
// entry_id being non-nil, never stored separately.
func TestDeleteEntry_BehindBookedStayClearsTheLink(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	tripID, err := s.AddTrip(ctx, Trip{
		Name:      "Direct Delete Trip",
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
		Nights: 4, Points: 100,
	})
	if err != nil {
		t.Fatalf("AddStay: %v", err)
	}
	if err := s.BookTrip(ctx, tripID); err != nil {
		t.Fatalf("BookTrip: %v", err)
	}

	stays, err := s.ListStays(ctx, tripID)
	if err != nil {
		t.Fatalf("ListStays: %v", err)
	}
	entryID := findStay(t, stays, stayID).EntryID
	if entryID == nil {
		t.Fatal("expected stay to be booked before direct DeleteEntry")
	}

	if err := s.DeleteEntry(ctx, *entryID); err != nil {
		t.Fatalf("DeleteEntry: %v", err)
	}

	stays, err = s.ListStays(ctx, tripID)
	if err != nil {
		t.Fatalf("ListStays after direct DeleteEntry: %v", err)
	}
	got := findStay(t, stays, stayID)
	if got.EntryID != nil {
		t.Errorf("stay.EntryID after direct DeleteEntry = %v, want nil (ON DELETE SET NULL)", got.EntryID)
	}

	if _, err := s.GetTrip(ctx, tripID); err != nil {
		t.Errorf("GetTrip after direct DeleteEntry: %v, want the trip to still exist", err)
	}
}
