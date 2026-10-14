package ledger

import (
	"context"
	"errors"
	"testing"
)

func importFixture(t *testing.T, s *Store) (Trip, []int64) {
	t.Helper()
	ctx := context.Background()
	var ids []int64
	for _, u := range []int{50, 30, 20} {
		id, err := s.AddEntry(ctx, Entry{UseYear: 2025, Date: date(t, "2025-03-01"), Kind: KindUsage, Used: u})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	trip := Trip{Name: "Past", StartDate: date(t, "2025-03-01"), EndDate: date(t, "2025-03-08"), MinNights: 1}
	return trip, ids
}

func stayOf(t *testing.T, points int) TripStay {
	return TripStay{Resort: "R", RoomType: "Studio", View: "Std", CheckIn: date(t, "2025-03-01"), CheckOut: date(t, "2025-03-05"), Nights: 4, Points: points}
}

func TestImportBookedTripLinksExistingEntries(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	trip, ids := importFixture(t, s)
	before, _ := s.ListEntries(ctx)

	id, err := s.ImportBookedTrip(ctx, trip, []ImportedStay{
		{Stay: stayOf(t, 80), EntryIDs: ids[:2]},
		{Stay: stayOf(t, 20), EntryIDs: ids[2:]},
	})
	if err != nil {
		t.Fatalf("ImportBookedTrip: %v", err)
	}
	stays, err := s.ListStays(ctx, id)
	if err != nil || len(stays) != 2 {
		t.Fatalf("stays=%v err=%v", stays, err)
	}
	for _, st := range stays {
		if !st.Booked() || st.PartiallyBooked() {
			t.Errorf("stay %+v should be fully booked", st)
		}
	}
	after, _ := s.ListEntries(ctx)
	if len(after) != len(before) {
		t.Errorf("entries changed: %d -> %d", len(before), len(after))
	}
	if err := s.UnbookTrip(ctx, id); err != nil {
		t.Fatal(err)
	}
	if left, _ := s.ListEntries(ctx); len(left) != 0 {
		t.Errorf("unbook should delete linked entries, %d left", len(left))
	}
}

func TestImportBookedTripValidation(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name  string
		stays func(t *testing.T, s *Store, ids []int64) []ImportedStay
		want  error
	}{
		{"no entries", func(t *testing.T, s *Store, ids []int64) []ImportedStay {
			return []ImportedStay{{Stay: stayOf(t, 80)}}
		}, ErrImportNoEntries},
		{"missing entry", func(t *testing.T, s *Store, ids []int64) []ImportedStay {
			return []ImportedStay{{Stay: stayOf(t, 80), EntryIDs: []int64{ids[0], 999999}}}
		}, ErrImportEntryNotFound},
		{"not usage", func(t *testing.T, s *Store, ids []int64) []ImportedStay {
			a, _ := s.AddEntry(ctx, Entry{UseYear: 2025, Date: date(t, "2025-03-01"), Kind: KindAllocation, Allotted: 80})
			return []ImportedStay{{Stay: stayOf(t, 80), EntryIDs: []int64{a}}}
		}, ErrImportEntryNotUsage},
		{"duplicate id", func(t *testing.T, s *Store, ids []int64) []ImportedStay {
			return []ImportedStay{{Stay: stayOf(t, 100), EntryIDs: []int64{ids[0], ids[0]}}}
		}, ErrImportDuplicateEntry},
		{"duplicate across stays", func(t *testing.T, s *Store, ids []int64) []ImportedStay {
			return []ImportedStay{{Stay: stayOf(t, 50), EntryIDs: ids[:1]}, {Stay: stayOf(t, 50), EntryIDs: ids[:1]}}
		}, ErrImportDuplicateEntry},
		{"sum mismatch", func(t *testing.T, s *Store, ids []int64) []ImportedStay {
			return []ImportedStay{{Stay: stayOf(t, 81), EntryIDs: ids[:2]}}
		}, ErrImportPointsMismatch},
		{"nights mismatch", func(t *testing.T, s *Store, ids []int64) []ImportedStay {
			st := stayOf(t, 80)
			st.Nights = 3
			return []ImportedStay{{Stay: st, EntryIDs: ids[:2]}}
		}, ErrImportInvalid},
		{"no stays", func(t *testing.T, s *Store, ids []int64) []ImportedStay { return nil }, ErrImportInvalid},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := openTestStore(t)
			trip, ids := importFixture(t, s)
			_, err := s.ImportBookedTrip(ctx, trip, tc.stays(t, s, ids))
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if trips, _ := s.ListTrips(ctx); len(trips) != 0 {
				t.Errorf("rollback failed, %d trips", len(trips))
			}
		})
	}
}

func TestImportBookedTripRejectsAlreadyLinkedAndRollsBack(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	trip, ids := importFixture(t, s)
	if _, err := s.ImportBookedTrip(ctx, trip, []ImportedStay{{Stay: stayOf(t, 80), EntryIDs: ids[:2]}}); err != nil {
		t.Fatal(err)
	}
	// second import: first stay fine, second reuses a linked entry
	_, err := s.ImportBookedTrip(ctx, trip, []ImportedStay{
		{Stay: stayOf(t, 20), EntryIDs: ids[2:]},
		{Stay: stayOf(t, 50), EntryIDs: ids[:1]},
	})
	if !errors.Is(err, ErrImportEntryLinked) {
		t.Fatalf("err = %v, want ErrImportEntryLinked", err)
	}
	if trips, _ := s.ListTrips(ctx); len(trips) != 1 {
		t.Errorf("trips = %d, want 1 (second import rolled back)", len(trips))
	}
}

func TestImportBookedTripRejectsZeroUsed(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	trip, _ := importFixture(t, s)
	z, _ := s.AddEntry(ctx, Entry{UseYear: 2025, Date: date(t, "2025-03-01"), Kind: KindUsage, Used: 0})
	_, err := s.ImportBookedTrip(ctx, trip, []ImportedStay{{Stay: stayOf(t, 0), EntryIDs: []int64{z}}})
	if !errors.Is(err, ErrImportEntryNotUsage) {
		t.Fatalf("err = %v", err)
	}
}
