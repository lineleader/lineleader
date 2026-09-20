package ledger

import (
	"context"
	"errors"
	"sync"
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

// findEntriesByDesc returns every entry whose Desc matches want, in
// ListEntries' own (date, id) order. A stay funded from several lots now
// produces several entries sharing one Desc, so findEntryByDesc's "exactly
// one" requirement doesn't fit those — this is its unfiltered counterpart.
func findEntriesByDesc(entries []Entry, want string) []Entry {
	var found []Entry
	for _, e := range entries {
		if e.Desc == want {
			found = append(found, e)
		}
	}
	return found
}

// usageEntries filters entries down to Kind == KindUsage, ignoring the
// allocation entries a test posts to fund a lot. Those never get touched by
// BookTrip, UnbookTrip, DeleteTrip or DeleteStay, and would otherwise
// pollute a bare len(entries) count in tests below that seed one.
func usageEntries(entries []Entry) []Entry {
	var out []Entry
	for _, e := range entries {
		if e.Kind == KindUsage {
			out = append(out, e)
		}
	}
	return out
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

// addContract inserts a minimal contract anchored to useYearMonth and
// returns its id. AnnualPoints is irrelevant to the allocator — it only
// ever draws from real posted lots (see PointLot's doc comment) — so tests
// that need BookTrip to actually fund a stay post one with mustAddAlloc
// (distribute_test.go), not by setting AnnualPoints here.
func addContract(t *testing.T, s *Store, useYearMonth time.Month) int64 {
	t.Helper()
	id, err := s.AddContract(context.Background(), Contract{
		Name:         "C",
		UseYearMonth: useYearMonth,
	})
	if err != nil {
		t.Fatalf("AddContract: %v", err)
	}
	return id
}

// TestBookTrip_WritesOneEntryPerStay is the basic happy path: two unbooked
// stays on a trip, each fully funded from one contract's single posted lot,
// book them both and check every field BookTrip is responsible for shaping
// — not just that an entry got created. Since each stay's need is covered
// by the FIRST candidate lot AllocateStayPoints considers, this also proves
// requirement nyj.4.1: a stay funded from one lot writes exactly one entry,
// carrying that entry's contract, use year and tag.
func TestBookTrip_WritesOneEntryPerStay(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	cid := addContract(t, s, time.January)
	mustAddAlloc(t, s, cid, 2026, 300) // covers both stays (120 + 160) with headroom

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
	if usage := usageEntries(entries); len(usage) != 2 {
		t.Fatalf("len(usage entries) = %d, want 2: %+v", len(usage), usage)
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
		t.Errorf("e1.UseYear = %d, want 2026", e1.UseYear)
	}
	if e1.Tag != "" {
		t.Errorf("e1.Tag = %q, want empty (current-year draw)", e1.Tag)
	}
	if e1.ContractID == nil || *e1.ContractID != cid {
		t.Errorf("e1.ContractID = %v, want %d", e1.ContractID, cid)
	}

	e2 := findEntryByDesc(t, entries, "Test Trip Book — Animal Kingdom Villas 1 Bedroom")
	if e2.Used != 160 {
		t.Errorf("e2.Used = %d, want 160", e2.Used)
	}
	if e2.UseYear != 2026 {
		t.Errorf("e2.UseYear = %d, want 2026", e2.UseYear)
	}
	if e2.ContractID == nil || *e2.ContractID != cid {
		t.Errorf("e2.ContractID = %v, want %d", e2.ContractID, cid)
	}

	stays, err := s.ListStays(ctx, tripID)
	if err != nil {
		t.Fatalf("ListStays: %v", err)
	}
	got1 := findStay(t, stays, stay1ID)
	if len(got1.EntryIDs) != 1 || got1.EntryIDs[0] != e1.ID {
		t.Errorf("stay1.EntryIDs = %v, want [%d]", got1.EntryIDs, e1.ID)
	}
	got2 := findStay(t, stays, stay2ID)
	if len(got2.EntryIDs) != 1 || got2.EntryIDs[0] != e2.ID {
		t.Errorf("stay2.EntryIDs = %v, want [%d]", got2.EntryIDs, e2.ID)
	}
}

// TestBookTrip_StayNeedsMultipleLots is the 95/55 worked example: a stay
// costing 150 points, funded by two same-rank (current) lots on different
// contracts — the first (95 points) covers what it can, and the allocator
// spills the remainder (55) into the second. This is requirement nyj.4.2:
// several entries whose Used sums to the stay's points.
func TestBookTrip_StayNeedsMultipleLots(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	c1 := addContract(t, s, time.January)
	c2 := addContract(t, s, time.January)
	mustAddAlloc(t, s, c1, 2026, 95)
	mustAddAlloc(t, s, c2, 2026, 55)

	tripID, err := s.AddTrip(ctx, Trip{
		Name:      "Multi-lot Trip",
		StartDate: date(t, "2026-06-01"),
		EndDate:   date(t, "2026-06-10"),
		MinNights: 1,
	})
	if err != nil {
		t.Fatalf("AddTrip: %v", err)
	}
	stayID, err := s.AddStay(ctx, TripStay{
		TripID: tripID, Resort: "BLT", RoomType: "Studio",
		CheckIn: date(t, "2026-06-01"), CheckOut: date(t, "2026-06-06"),
		Nights: 5, Points: 150,
	})
	if err != nil {
		t.Fatalf("AddStay: %v", err)
	}

	if err := s.BookTrip(ctx, tripID); err != nil {
		t.Fatalf("BookTrip: %v", err)
	}

	entries, err := s.ListEntries(ctx)
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	matches := findEntriesByDesc(entries, "Multi-lot Trip — BLT Studio")
	if len(matches) != 2 {
		t.Fatalf("entries for the stay = %d, want 2: %+v", len(matches), matches)
	}

	var sum int
	byContract := make(map[int64]Entry, 2)
	for _, e := range matches {
		sum += e.Used
		if e.ContractID == nil {
			t.Fatalf("entry %+v has nil ContractID", e)
		}
		byContract[*e.ContractID] = e
	}
	if sum != 150 {
		t.Errorf("sum of Used across draws = %d, want 150", sum)
	}
	if e, ok := byContract[c1]; !ok || e.Used != 95 || e.Tag != "" || e.UseYear != 2026 {
		t.Errorf("c1's draw = %+v (ok=%v), want Used=95 Tag=\"\" UseYear=2026", e, ok)
	}
	if e, ok := byContract[c2]; !ok || e.Used != 55 || e.Tag != "" || e.UseYear != 2026 {
		t.Errorf("c2's draw = %+v (ok=%v), want Used=55 Tag=\"\" UseYear=2026", e, ok)
	}

	stays, err := s.ListStays(ctx, tripID)
	if err != nil {
		t.Fatalf("ListStays: %v", err)
	}
	if got := findStay(t, stays, stayID); len(got.EntryIDs) != 2 {
		t.Errorf("stay.EntryIDs = %v, want 2 linked entries", got.EntryIDs)
	}
}

// TestBookTrip_DispositionTags proves banked, current and borrowed draws
// get tagged "Bank", "" and "Borrow" respectively (requirement nyj.4.4).
// One contract carries three lots — use year 2025 (banked relative to a
// 2026 check-in), 2026 (current) and 2027 (borrowed) — each with exactly 10
// points, and a 30-point stay is forced to draw from all three in rank
// order (banked, then current, then borrowed).
func TestBookTrip_DispositionTags(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	cid := addContract(t, s, time.January)
	mustAddAlloc(t, s, cid, 2025, 10)
	mustAddAlloc(t, s, cid, 2026, 10)
	mustAddAlloc(t, s, cid, 2027, 10)

	tripID, err := s.AddTrip(ctx, Trip{
		Name:      "Disposition Trip",
		StartDate: date(t, "2026-06-01"),
		EndDate:   date(t, "2026-06-10"),
		MinNights: 1,
	})
	if err != nil {
		t.Fatalf("AddTrip: %v", err)
	}
	if _, err := s.AddStay(ctx, TripStay{
		TripID: tripID, Resort: "BLT", RoomType: "Studio",
		CheckIn: date(t, "2026-06-01"), CheckOut: date(t, "2026-06-04"),
		Nights: 3, Points: 30,
	}); err != nil {
		t.Fatalf("AddStay: %v", err)
	}

	if err := s.BookTrip(ctx, tripID); err != nil {
		t.Fatalf("BookTrip: %v", err)
	}

	entries, err := s.ListEntries(ctx)
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	matches := findEntriesByDesc(entries, "Disposition Trip — BLT Studio")
	if len(matches) != 3 {
		t.Fatalf("entries for the stay = %d, want 3: %+v", len(matches), matches)
	}

	wantTagByUseYear := map[int]string{2025: "Bank", 2026: "", 2027: "Borrow"}
	seen := make(map[int]bool, 3)
	for _, e := range matches {
		if e.Used != 10 {
			t.Errorf("entry %+v Used = %d, want 10", e, e.Used)
		}
		if e.ContractID == nil || *e.ContractID != cid {
			t.Errorf("entry %+v ContractID = %v, want %d", e, e.ContractID, cid)
		}
		wantTag, ok := wantTagByUseYear[e.UseYear]
		if !ok {
			t.Fatalf("entry %+v has unexpected UseYear %d", e, e.UseYear)
		}
		if e.Tag != wantTag {
			t.Errorf("entry for use year %d Tag = %q, want %q", e.UseYear, e.Tag, wantTag)
		}
		seen[e.UseYear] = true
	}
	for year := range wantTagByUseYear {
		if !seen[year] {
			t.Errorf("no draw seen for use year %d", year)
		}
	}
}

// TestBookTrip_TwoStaysCannotDoubleSpendALot seeds exactly enough points for
// ONE stay and books a trip with two: the first stay's allocation walk (if
// it ran alone) would exactly exhaust the lot, so the second stay can only
// be funded if BookTrip mistakenly still sees those points as available —
// i.e. if it forgot to append the first stay's draws to `consumed` before
// allocating the second. BookTrip must instead fail the whole booking with
// ErrInsufficientPoints and write nothing (requirement nyj.4.3).
func TestBookTrip_TwoStaysCannotDoubleSpendALot(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	cid := addContract(t, s, time.January)
	mustAddAlloc(t, s, cid, 2026, 100)

	tripID, err := s.AddTrip(ctx, Trip{
		Name:      "Double Spend Trip",
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
		t.Fatalf("AddStay stay1: %v", err)
	}
	stay2ID, err := s.AddStay(ctx, TripStay{
		TripID: tripID, Resort: "AKV", RoomType: "1BR",
		CheckIn: date(t, "2026-06-05"), CheckOut: date(t, "2026-06-10"),
		Nights: 5, Points: 10,
	})
	if err != nil {
		t.Fatalf("AddStay stay2: %v", err)
	}

	err = s.BookTrip(ctx, tripID)
	if !errors.Is(err, ErrInsufficientPoints) {
		t.Fatalf("BookTrip err = %v, want ErrInsufficientPoints", err)
	}

	entries, err := s.ListEntries(ctx)
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	if usage := usageEntries(entries); len(usage) != 0 {
		t.Fatalf("usage entries after failed BookTrip = %+v, want empty", usage)
	}

	stays, err := s.ListStays(ctx, tripID)
	if err != nil {
		t.Fatalf("ListStays: %v", err)
	}
	if got := findStay(t, stays, stay1ID); got.Booked() {
		t.Errorf("stay1 EntryIDs = %v, want empty — the whole booking must have rolled back", got.EntryIDs)
	}
	if got := findStay(t, stays, stay2ID); got.Booked() {
		t.Errorf("stay2 EntryIDs = %v, want empty", got.EntryIDs)
	}
}

// TestBookTrip_InsufficientPointsRollsBackWholeTrip pins requirement
// nyj.4.5 with the simplest possible shortfall: the FIRST stay processed
// already needs more points than exist in total, so nothing is ever
// written, including for the second, never-reached stay.
func TestBookTrip_InsufficientPointsRollsBackWholeTrip(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	cid := addContract(t, s, time.January)
	mustAddAlloc(t, s, cid, 2026, 30)

	tripID, err := s.AddTrip(ctx, Trip{
		Name:      "Underfunded Trip",
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
		Nights: 4, Points: 50,
	})
	if err != nil {
		t.Fatalf("AddStay stay1: %v", err)
	}
	stay2ID, err := s.AddStay(ctx, TripStay{
		TripID: tripID, Resort: "AKV", RoomType: "1BR",
		CheckIn: date(t, "2026-06-05"), CheckOut: date(t, "2026-06-10"),
		Nights: 5, Points: 10,
	})
	if err != nil {
		t.Fatalf("AddStay stay2: %v", err)
	}

	err = s.BookTrip(ctx, tripID)
	if !errors.Is(err, ErrInsufficientPoints) {
		t.Fatalf("BookTrip err = %v, want ErrInsufficientPoints", err)
	}

	entries, err := s.ListEntries(ctx)
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	if usage := usageEntries(entries); len(usage) != 0 {
		t.Fatalf("usage entries after failed BookTrip = %+v, want empty", usage)
	}

	stays, err := s.ListStays(ctx, tripID)
	if err != nil {
		t.Fatalf("ListStays: %v", err)
	}
	if got := findStay(t, stays, stay1ID); got.Booked() {
		t.Errorf("stay1 EntryIDs = %v, want empty", got.EntryIDs)
	}
	if got := findStay(t, stays, stay2ID); got.Booked() {
		t.Errorf("stay2 EntryIDs = %v, want empty — never reached, but still must not be booked", got.EntryIDs)
	}
}

// TestBookTrip_PerStayUseYearAcrossBoundary is the rule that distinguishes
// per-stay from per-trip attribution: each entry's UseYear is computed from
// its OWN stay's check-in date against its contract's UseYearMonth, not
// from the trip's start date, because a trip window can straddle a use-year
// boundary even though the displayed budget cannot. With an April use-year
// start, a 2026-03-20 check-in is use year 2025 and a 2026-04-05 check-in —
// five days later, same trip — is use year 2026. Getting this wrong (e.g.
// stamping every entry with the trip's own use year) would silently
// misattribute one stay's points to the wrong year's balance.
func TestBookTrip_PerStayUseYearAcrossBoundary(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	cid := addContract(t, s, time.April)
	// The 2025 lot is funded for EXACTLY the before-boundary stay's 80
	// points, not a penny more: any leftover would still be eligible as a
	// BANKED draw for the after-boundary stay too (its own use year is
	// 2026, so 2025 = uy-1 for it as well), which the allocator would
	// happily sweep up first — splitting that stay's entry across two use
	// years and defeating this test's whole point.
	mustAddAlloc(t, s, cid, 2025, 80)  // funds the before-boundary stay's own use year, exactly
	mustAddAlloc(t, s, cid, 2026, 150) // funds the after-boundary stay's own use year

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
	if usage := usageEntries(entries); len(usage) != 2 {
		t.Fatalf("len(usage entries) = %d, want 2: %+v", len(usage), usage)
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

	cid := addContract(t, s, time.January)
	mustAddAlloc(t, s, cid, 2026, 300) // covers both stays (100 + 150)

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
	entryIDsBefore1 := findStay(t, staysBefore, stay1ID).EntryIDs
	entryIDsBefore2 := findStay(t, staysBefore, stay2ID).EntryIDs
	if len(entryIDsBefore1) != 1 || len(entryIDsBefore2) != 1 {
		t.Fatalf("expected both stays booked with exactly one entry after first BookTrip: %+v", staysBefore)
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
	entryIDsAfter1 := findStay(t, staysAfter, stay1ID).EntryIDs
	entryIDsAfter2 := findStay(t, staysAfter, stay2ID).EntryIDs
	if len(entryIDsAfter1) != 1 || entryIDsAfter1[0] != entryIDsBefore1[0] {
		t.Errorf("stay1 EntryIDs changed on re-book: before %v, after %v", entryIDsBefore1, entryIDsAfter1)
	}
	if len(entryIDsAfter2) != 1 || entryIDsAfter2[0] != entryIDsBefore2[0] {
		t.Errorf("stay2 EntryIDs changed on re-book: before %v, after %v", entryIDsBefore2, entryIDsAfter2)
	}
}

// TestBookTrip_RollbackIsAllOrNothing is the atomicity proof: BookTrip must
// run every stay's writes inside one transaction, not one transaction per
// stay. We install a temporary CHECK constraint that only the SECOND
// stay's computed description violates (stays are processed in ListStays'
// (check_in, id) order, which is what makes "second" deterministic here).
// Both stays are funded generously, so the allocator itself has no
// objection — the first stay's insert succeeds inside the doomed
// transaction, then the second fails the CHECK. If BookTrip committed
// per-stay instead of once for the whole trip, the first stay's entry
// would survive; it must not.
func TestBookTrip_RollbackIsAllOrNothing(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	cid := addContract(t, s, time.January)
	mustAddAlloc(t, s, cid, 2026, 300) // covers both stays (100 + 150) with headroom

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
	if usage := usageEntries(entries); len(usage) != 0 {
		t.Fatalf("usage entries after failed BookTrip = %+v, want empty — the first stay's insert must have rolled back too", usage)
	}

	stays, err := s.ListStays(ctx, tripID)
	if err != nil {
		t.Fatalf("ListStays: %v", err)
	}
	first := findStay(t, stays, firstStayID)
	if first.Booked() {
		t.Errorf("first stay EntryIDs = %v, want empty — its insert must not have survived the rolled-back transaction", first.EntryIDs)
	}
}

// TestBookTrip_ConcurrentBookingOnlyOneSucceeds is the first committed test
// of lotSnapshot's lock=true path: two trips, each with one stay needing 60
// of a shared contract's only 100 posted points, booked from two goroutines
// at once. Exactly one must succeed; the other must see
// ErrInsufficientPoints, never a raw driver/serialization error — BookTrip's
// retry loop re-reads the now-committed balances on the loser's retry, and
// AllocateStayPoints reports the shortfall itself (see BookTrip's doc
// comment). The ledger must end up with exactly one booking's worth of
// points drawn, never both (overdrawn) and never neither.
func TestBookTrip_ConcurrentBookingOnlyOneSucceeds(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	cid := addContract(t, s, time.January)
	mustAddAlloc(t, s, cid, 2026, 100)

	newRacingTrip := func(name string) int64 {
		tripID, err := s.AddTrip(ctx, Trip{
			Name:      name,
			StartDate: date(t, "2026-06-01"),
			EndDate:   date(t, "2026-06-10"),
			MinNights: 1,
		})
		if err != nil {
			t.Fatalf("AddTrip(%s): %v", name, err)
		}
		if _, err := s.AddStay(ctx, TripStay{
			TripID: tripID, Resort: "BLT", RoomType: "Studio",
			CheckIn: date(t, "2026-06-01"), CheckOut: date(t, "2026-06-05"),
			Nights: 4, Points: 60,
		}); err != nil {
			t.Fatalf("AddStay(%s): %v", name, err)
		}
		return tripID
	}

	tripA := newRacingTrip("Race A")
	tripB := newRacingTrip("Race B")

	var wg sync.WaitGroup
	var errA, errB error
	ready := make(chan struct{})
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-ready
		errA = s.BookTrip(ctx, tripA)
	}()
	go func() {
		defer wg.Done()
		<-ready
		errB = s.BookTrip(ctx, tripB)
	}()
	close(ready)
	wg.Wait()

	succeeded := 0
	for _, err := range []error{errA, errB} {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrInsufficientPoints):
			// the expected loser
		default:
			t.Errorf("unexpected error racing to book: %v", err)
		}
	}
	if succeeded != 1 {
		t.Fatalf("succeeded = %d, want exactly 1 (errA=%v, errB=%v)", succeeded, errA, errB)
	}

	entries, err := s.ListEntries(ctx)
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	var totalUsed int
	for _, e := range entries {
		totalUsed += e.Used
	}
	if totalUsed != 60 {
		t.Errorf("total Used across the ledger = %d, want 60 (exactly one booking's worth — must not be overdrawn)", totalUsed)
	}
}

// TestBookTrip_DistributeIdempotencyUnaffectedByUsageContractID pins
// requirement nyj.4.7: BookTrip's usage entries now carry a contract_id,
// same as an allocation entry, but distributeUpTo's queries
// (LatestAllocationYear, CountAllocationFor) filter on kind = 'allocation'
// — a booked usage entry sharing that contract_id must not be mistaken for
// an already-posted allocation, in either direction (missing a year that
// should be created, or falsely believing one already was).
func TestBookTrip_DistributeIdempotencyUnaffectedByUsageContractID(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	cid := addContract(t, s, time.January)
	mustAddAlloc(t, s, cid, 2026, 100)

	tripID, err := s.AddTrip(ctx, Trip{
		Name:      "Distribute Check Trip",
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
		Nights: 4, Points: 40,
	}); err != nil {
		t.Fatalf("AddStay: %v", err)
	}
	if err := s.BookTrip(ctx, tripID); err != nil {
		t.Fatalf("BookTrip: %v", err)
	}

	created, err := s.distributeUpTo(ctx, 2027)
	if err != nil {
		t.Fatalf("distributeUpTo: %v", err)
	}
	if len(created) != 1 {
		t.Fatalf("distributeUpTo created %d rows, want 1 (the 2027 allocation): %+v", len(created), created)
	}
	if created[0].UseYear != 2027 || created[0].Kind != KindAllocation {
		t.Errorf("created entry = %+v, want use year 2027 allocation", created[0])
	}

	created2, err := s.distributeUpTo(ctx, 2027)
	if err != nil {
		t.Fatalf("second distributeUpTo: %v", err)
	}
	if len(created2) != 0 {
		t.Fatalf("second distributeUpTo created %d rows, want 0 (idempotent)", len(created2))
	}
}

// TestUnbookTrip_RemovesLinkedEntriesOnly seeds one unrelated, hand-entered
// usage entry (not attached to any stay) alongside a booked trip, then
// unbooks the trip. Only the entries BookTrip created for this trip's
// stays should disappear; the unrelated entry — and the stays themselves —
// must survive.
func TestUnbookTrip_RemovesLinkedEntriesOnly(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	cid := addContract(t, s, time.January)
	mustAddAlloc(t, s, cid, 2026, 300) // covers both stays (100 + 150)

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

	staysBooked, err := s.ListStays(ctx, tripID)
	if err != nil {
		t.Fatalf("ListStays (booked): %v", err)
	}
	var tripEntryIDs []int64
	for _, st := range staysBooked {
		tripEntryIDs = append(tripEntryIDs, st.EntryIDs...)
	}
	if len(tripEntryIDs) == 0 {
		t.Fatal("precondition: trip has no booked entries")
	}

	if err := s.UnbookTrip(ctx, tripID); err != nil {
		t.Fatalf("UnbookTrip: %v", err)
	}

	entries, err := s.ListEntries(ctx)
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	byID := make(map[int64]bool, len(entries))
	for _, e := range entries {
		byID[e.ID] = true
	}
	if !byID[unrelatedID] {
		t.Error("unrelated entry did not survive UnbookTrip")
	}
	for _, id := range tripEntryIDs {
		if byID[id] {
			t.Errorf("trip entry %d still present after UnbookTrip, want deleted", id)
		}
	}
	// The funding allocation entry is untouched by UnbookTrip — only the
	// unrelated hand-entered usage entry and that allocation should remain.
	const wantRemaining = 2 // unrelated usage entry + the funding allocation
	if len(entries) != wantRemaining {
		t.Errorf("ListEntries after UnbookTrip = %+v, want %d entries (the unrelated one plus the funding allocation)", entries, wantRemaining)
	}

	stays, err := s.ListStays(ctx, tripID)
	if err != nil {
		t.Fatalf("ListStays: %v", err)
	}
	if got := findStay(t, stays, stay1ID); got.Booked() {
		t.Errorf("stay1.EntryIDs = %v, want empty after UnbookTrip", got.EntryIDs)
	}
	if got := findStay(t, stays, stay2ID); got.Booked() {
		t.Errorf("stay2.EntryIDs = %v, want empty after UnbookTrip", got.EntryIDs)
	}
}

// TestDeleteTrip_LeavesNoOrphanEntries is the sharpest trap in this
// feature. DeleteTrip must delete the trip's ledger entries BEFORE deleting
// the trip row, because trip_stay cascades away (ON DELETE CASCADE) the
// instant the trip is deleted — and once trip_stay is gone, so is the only
// link (trip_stay_entry) that could find those entries again. Deleting the
// trip first would strand the usage rows in the ledger forever, with no way
// to identify or reverse them. This test seeds an unrelated entry to prove
// the deletion is scoped correctly, not just that "some" entries disappear.
func TestDeleteTrip_LeavesNoOrphanEntries(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	cid := addContract(t, s, time.January)
	mustAddAlloc(t, s, cid, 2026, 300) // covers both stays (100 + 150)

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

	staysBooked, err := s.ListStays(ctx, tripID)
	if err != nil {
		t.Fatalf("ListStays before delete: %v", err)
	}
	var tripEntryIDs []int64
	for _, st := range staysBooked {
		tripEntryIDs = append(tripEntryIDs, st.EntryIDs...)
	}
	if len(tripEntryIDs) != 2 {
		t.Fatalf("expected 2 trip entries before delete, got %d: %+v", len(tripEntryIDs), staysBooked)
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

// TestDeleteStay_RemovesOnlyThatStayAndItsEntries books a trip whose first
// stay is funded from TWO lots (a banked lot plus a spill into the current
// lot) and whose second stay is funded from just one, then deletes the
// first stay. The deletion must remove BOTH of the first stay's entries —
// DeleteStay reaches them only through trip_stay_entry, with no LIMIT on
// how many rows one stay can have — while the second stay and its own
// single entry stay completely untouched.
func TestDeleteStay_RemovesOnlyThatStayAndItsEntries(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	cid := addContract(t, s, time.January)
	mustAddAlloc(t, s, cid, 2025, 40)  // banked relative to the June 2026 check-ins
	mustAddAlloc(t, s, cid, 2026, 250) // current; big enough to cover both stays' remainders

	tripID, err := s.AddTrip(ctx, Trip{
		Name:      "Stay Delete Trip",
		StartDate: date(t, "2026-06-01"),
		EndDate:   date(t, "2026-06-10"),
		MinNights: 1,
	})
	if err != nil {
		t.Fatalf("AddTrip: %v", err)
	}
	// First by (check_in, id): draws the 40-point banked lot fully, then
	// spills 60 more into the current lot — two entries.
	stay1ID, err := s.AddStay(ctx, TripStay{
		TripID: tripID, Resort: "BLT", RoomType: "Studio",
		CheckIn: date(t, "2026-06-01"), CheckOut: date(t, "2026-06-05"),
		Nights: 4, Points: 100,
	})
	if err != nil {
		t.Fatalf("AddStay stay1: %v", err)
	}
	// Second by (check_in, id): the banked lot is exhausted by then, so this
	// draws entirely from the current lot — one entry.
	stay2ID, err := s.AddStay(ctx, TripStay{
		TripID: tripID, Resort: "AKV", RoomType: "1BR",
		CheckIn: date(t, "2026-06-05"), CheckOut: date(t, "2026-06-10"),
		Nights: 5, Points: 150,
	})
	if err != nil {
		t.Fatalf("AddStay stay2: %v", err)
	}
	if err := s.BookTrip(ctx, tripID); err != nil {
		t.Fatalf("BookTrip: %v", err)
	}

	staysBefore, err := s.ListStays(ctx, tripID)
	if err != nil {
		t.Fatalf("ListStays before delete: %v", err)
	}
	stay1EntryIDs := findStay(t, staysBefore, stay1ID).EntryIDs
	stay2EntryIDs := findStay(t, staysBefore, stay2ID).EntryIDs
	if len(stay1EntryIDs) != 2 {
		t.Fatalf("precondition: stay1 must be booked with 2 entries, got %v", stay1EntryIDs)
	}
	if len(stay2EntryIDs) != 1 {
		t.Fatalf("precondition: stay2 must be booked with 1 entry, got %v", stay2EntryIDs)
	}
	stay2EntryID := stay2EntryIDs[0]

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
	if len(remaining.EntryIDs) != 1 || remaining.EntryIDs[0] != stay2EntryID {
		t.Errorf("stay2.EntryIDs after DeleteStay = %v, want unchanged [%d]", remaining.EntryIDs, stay2EntryID)
	}

	entries, err := s.ListEntries(ctx)
	if err != nil {
		t.Fatalf("ListEntries after delete: %v", err)
	}
	byID := make(map[int64]bool, len(entries))
	for _, e := range entries {
		byID[e.ID] = true
	}
	for _, id := range stay1EntryIDs {
		if byID[id] {
			t.Errorf("stay1's entry %d still present after DeleteStay", id)
		}
	}
	if !byID[stay2EntryID] {
		t.Errorf("stay2's entry (%d) missing after DeleteStay; it should be untouched", stay2EntryID)
	}
	if usage := usageEntries(entries); len(usage) != 1 {
		t.Errorf("usage entries after DeleteStay = %+v, want exactly stay2's one entry", usage)
	}
}

// TestDeleteEntry_BehindBookedStayClearsTheLink documents that
// trip_stay_entry.entry_id's ON DELETE CASCADE is CORRECT behavior, not a
// bug: deleting a ledger entry directly on /ledger (bypassing UnbookTrip or
// DeleteStay entirely) must not leave a dangling foreign key or break the
// stay. The stay and its trip both keep existing; the stay's booked-ness
// simply reverts to "not booked" because that status is always derived from
// whether it has any linked entries (TripStay.Booked), never stored
// separately.
func TestDeleteEntry_BehindBookedStayClearsTheLink(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	cid := addContract(t, s, time.January)
	mustAddAlloc(t, s, cid, 2026, 150)

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
	entryIDs := findStay(t, stays, stayID).EntryIDs
	if len(entryIDs) != 1 {
		t.Fatal("expected stay to be booked with exactly one entry before direct DeleteEntry")
	}

	if err := s.DeleteEntry(ctx, entryIDs[0]); err != nil {
		t.Fatalf("DeleteEntry: %v", err)
	}

	stays, err = s.ListStays(ctx, tripID)
	if err != nil {
		t.Fatalf("ListStays after direct DeleteEntry: %v", err)
	}
	got := findStay(t, stays, stayID)
	if got.Booked() {
		t.Errorf("stay.EntryIDs after direct DeleteEntry = %v, want empty (ON DELETE CASCADE removes the link row)", got.EntryIDs)
	}

	if _, err := s.GetTrip(ctx, tripID); err != nil {
		t.Errorf("GetTrip after direct DeleteEntry: %v, want the trip to still exist", err)
	}
}
