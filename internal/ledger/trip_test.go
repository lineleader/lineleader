package ledger

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestTripCRUD(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	budget := 500
	trip := Trip{
		Name:           "WDW June",
		StartDate:      date(t, "2026-06-01"),
		EndDate:        date(t, "2026-06-10"),
		MinNights:      3,
		BudgetOverride: &budget,
		FilterMode:     TripFilterInherit,
	}
	id, err := s.AddTrip(ctx, trip)
	if err != nil {
		t.Fatalf("AddTrip: %v", err)
	}

	got, err := s.GetTrip(ctx, id)
	if err != nil {
		t.Fatalf("GetTrip: %v", err)
	}
	if got.Name != trip.Name || got.MinNights != 3 {
		t.Errorf("round-trip mismatch: %+v", got)
	}
	if !got.StartDate.Equal(trip.StartDate) || !got.EndDate.Equal(trip.EndDate) {
		t.Errorf("dates = %v..%v, want %v..%v", got.StartDate, got.EndDate, trip.StartDate, trip.EndDate)
	}
	if got.BudgetOverride == nil || *got.BudgetOverride != 500 {
		t.Errorf("BudgetOverride = %v, want *500", got.BudgetOverride)
	}

	list, err := s.ListTrips(ctx)
	if err != nil {
		t.Fatalf("ListTrips: %v", err)
	}
	if len(list) != 1 || list[0].ID != id {
		t.Fatalf("ListTrips = %+v, want one trip with id %d", list, id)
	}

	// Update: clear the override, changing BudgetOverride from set to nil.
	upd := got
	upd.Name = "WDW June (updated)"
	upd.BudgetOverride = nil
	if err := s.UpdateTrip(ctx, upd); err != nil {
		t.Fatalf("UpdateTrip: %v", err)
	}
	got, err = s.GetTrip(ctx, id)
	if err != nil {
		t.Fatalf("GetTrip after update: %v", err)
	}
	if got.Name != "WDW June (updated)" {
		t.Errorf("Name after update = %q, want %q", got.Name, "WDW June (updated)")
	}
	if got.BudgetOverride != nil {
		t.Errorf("BudgetOverride after update = %v, want nil", got.BudgetOverride)
	}
}

func TestGetTrip_NotFound(t *testing.T) {
	s := openTestStore(t)

	_, err := s.GetTrip(context.Background(), 999999)
	if !errors.Is(err, ErrTripNotFound) {
		t.Fatalf("GetTrip on missing id: err = %v, want ErrTripNotFound", err)
	}
}

func TestTripStayCRUD(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	tripID, err := s.AddTrip(ctx, Trip{
		Name:      "Host trip",
		StartDate: date(t, "2026-06-01"),
		EndDate:   date(t, "2026-06-10"),
		MinNights: 1,
	})
	if err != nil {
		t.Fatalf("AddTrip: %v", err)
	}

	unbooked := TripStay{
		TripID:    tripID,
		Resort:    "Bay Lake Tower",
		RoomType:  "Studio",
		View:      "Standard",
		CheckIn:   date(t, "2026-06-01"),
		CheckOut:  date(t, "2026-06-05"),
		Nights:    4,
		Points:    120,
		QuoteHash: QuoteHash("BLT", 2026, 0, []int{10, 20, 30, 40}),
	}
	id1, err := s.AddStay(ctx, unbooked)
	if err != nil {
		t.Fatalf("AddStay (unbooked): %v", err)
	}

	entryID, err := s.AddEntry(ctx, Entry{
		UseYear: 2026,
		Date:    date(t, "2026-06-05"),
		Desc:    "BLT 1BR",
		Kind:    KindUsage,
		Used:    130,
	})
	if err != nil {
		t.Fatalf("AddEntry: %v", err)
	}
	booked := TripStay{
		TripID:    tripID,
		Resort:    "Bay Lake Tower",
		RoomType:  "1 Bedroom",
		View:      "Lake",
		CheckIn:   date(t, "2026-06-05"),
		CheckOut:  date(t, "2026-06-10"),
		Nights:    5,
		Points:    130,
		QuoteHash: QuoteHash("BLT", 2026, 1, []int{20, 25, 30, 35, 40}),
		EntryID:   &entryID,
	}
	id2, err := s.AddStay(ctx, booked)
	if err != nil {
		t.Fatalf("AddStay (booked): %v", err)
	}

	got, err := s.ListStays(ctx, tripID)
	if err != nil {
		t.Fatalf("ListStays: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	// ListStays orders by (check_in, id); unbooked (check_in 06-01) sorts
	// before booked (check_in 06-05).
	if got[0].ID != id1 || got[0].EntryID != nil {
		t.Errorf("stay 0 = %+v, want unbooked stay id %d with nil EntryID", got[0], id1)
	}
	if got[1].ID != id2 || got[1].EntryID == nil || *got[1].EntryID != entryID {
		t.Errorf("stay 1 = %+v, want booked stay id %d linked to entry %d", got[1], id2, entryID)
	}
	if got[1].QuoteHash != booked.QuoteHash {
		t.Errorf("QuoteHash = %q, want %q", got[1].QuoteHash, booked.QuoteHash)
	}
	if !got[0].CheckIn.Equal(unbooked.CheckIn) || !got[0].CheckOut.Equal(unbooked.CheckOut) {
		t.Errorf("stay 0 dates = %v..%v, want %v..%v", got[0].CheckIn, got[0].CheckOut, unbooked.CheckIn, unbooked.CheckOut)
	}
}

func TestTripFilterJSONRoundTrip(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	t.Run("populated exclusion lists survive write-then-read", func(t *testing.T) {
		id, err := s.AddTrip(ctx, Trip{
			Name:             "Filtered",
			StartDate:        date(t, "2026-06-01"),
			EndDate:          date(t, "2026-06-10"),
			MinNights:        1,
			FilterMode:       TripFilterOverride,
			ExcludeResorts:   []string{"AKV", "BLT"},
			ExcludeRoomTypes: []string{"Studio", "1BR"},
		})
		if err != nil {
			t.Fatalf("AddTrip: %v", err)
		}
		got, err := s.GetTrip(ctx, id)
		if err != nil {
			t.Fatalf("GetTrip: %v", err)
		}
		if !reflect.DeepEqual(got.ExcludeResorts, []string{"AKV", "BLT"}) {
			t.Errorf("ExcludeResorts = %v, want [AKV BLT]", got.ExcludeResorts)
		}
		if !reflect.DeepEqual(got.ExcludeRoomTypes, []string{"Studio", "1BR"}) {
			t.Errorf("ExcludeRoomTypes = %v, want [Studio 1BR]", got.ExcludeRoomTypes)
		}
		if got.FilterMode != TripFilterOverride {
			t.Errorf("FilterMode = %q, want %q", got.FilterMode, TripFilterOverride)
		}
	})

	t.Run("defaults read back as nil, not error", func(t *testing.T) {
		id, err := s.AddTrip(ctx, Trip{
			Name:      "Default filters",
			StartDate: date(t, "2026-07-01"),
			EndDate:   date(t, "2026-07-10"),
			MinNights: 1,
		})
		if err != nil {
			t.Fatalf("AddTrip: %v", err)
		}
		got, err := s.GetTrip(ctx, id)
		if err != nil {
			t.Fatalf("GetTrip: %v", err)
		}
		if got.ExcludeResorts != nil {
			t.Errorf("ExcludeResorts = %#v, want nil", got.ExcludeResorts)
		}
		if got.ExcludeRoomTypes != nil {
			t.Errorf("ExcludeRoomTypes = %#v, want nil", got.ExcludeRoomTypes)
		}
		if got.FilterMode != TripFilterInherit {
			t.Errorf("FilterMode = %q, want %q (inherit)", got.FilterMode, TripFilterInherit)
		}
	})
}

func TestQuoteHash(t *testing.T) {
	base := QuoteHash("BLT", 2026, 0, []int{10, 12, 14, 16})

	t.Run("stable for identical inputs", func(t *testing.T) {
		if again := QuoteHash("BLT", 2026, 0, []int{10, 12, 14, 16}); again != base {
			t.Errorf("hash not stable: %q != %q", again, base)
		}
	})

	t.Run("differs by resort code", func(t *testing.T) {
		if h := QuoteHash("AKV", 2026, 0, []int{10, 12, 14, 16}); h == base {
			t.Error("hash unchanged when resort code differs")
		}
	})

	t.Run("differs by year", func(t *testing.T) {
		if h := QuoteHash("BLT", 2027, 0, []int{10, 12, 14, 16}); h == base {
			t.Error("hash unchanged when year differs")
		}
	})

	t.Run("differs by column index", func(t *testing.T) {
		if h := QuoteHash("BLT", 2026, 1, []int{10, 12, 14, 16}); h == base {
			t.Error("hash unchanged when column index differs")
		}
	})

	t.Run("differs by nightly points", func(t *testing.T) {
		if h := QuoteHash("BLT", 2026, 0, []int{10, 12, 14, 17}); h == base {
			t.Error("hash unchanged when nightly points differ")
		}
	})

	t.Run("differs when the same numbers regroup", func(t *testing.T) {
		a := QuoteHash("BLT", 2026, 0, []int{1, 23})
		b := QuoteHash("BLT", 2026, 0, []int{12, 3})
		if a == b {
			t.Errorf("QuoteHash([1,23]) == QuoteHash([12,3]) = %q; a separator-based encoding must distinguish regroupings", a)
		}
	})
}

// assertCheckViolation fails unless err is specifically a Postgres CHECK
// constraint violation (SQLSTATE 23514).
//
// Asserting merely that err != nil is not enough, and this file previously
// made that mistake: with a bare nil check, every subtest below still
// passed when the INSERT's column list was corrupted, because the
// statement then failed with "column does not exist" (42703) and a test
// looking only for "some error" cannot tell the two apart. That would let
// a dropped CHECK constraint ship green. Pinning the SQLSTATE means these
// tests fail loudly if the statement stops reaching the constraint it is
// meant to exercise.
func assertCheckViolation(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected a CHECK constraint violation, got nil error")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("expected a *pgconn.PgError, got %T: %v", err, err)
	}
	if pgErr.Code != "23514" {
		t.Fatalf("expected SQLSTATE 23514 (check_violation), got %s (%s): %v",
			pgErr.Code, pgErr.ConstraintName, err)
	}
}

func TestTripCheckConstraints(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	insert := `INSERT INTO trip (name, start_date, end_date, min_nights, budget_override, filter_mode) VALUES ($1, $2, $3, $4, $5, $6)`

	t.Run("start_date must be before end_date", func(t *testing.T) {
		_, err := s.db.ExecContext(ctx, insert,
			"Bad dates", date(t, "2026-06-10"), date(t, "2026-06-01"), 1, nil, "")
		assertCheckViolation(t, err)
	})

	t.Run("min_nights must be at least 1", func(t *testing.T) {
		_, err := s.db.ExecContext(ctx, insert,
			"Bad min_nights", date(t, "2026-06-01"), date(t, "2026-06-10"), 0, nil, "")
		assertCheckViolation(t, err)
	})

	t.Run("min_nights must be at most 30", func(t *testing.T) {
		_, err := s.db.ExecContext(ctx, insert,
			"Bad min_nights", date(t, "2026-06-01"), date(t, "2026-06-10"), 31, nil, "")
		assertCheckViolation(t, err)
	})

	t.Run("budget_override must be nil or non-negative", func(t *testing.T) {
		_, err := s.db.ExecContext(ctx, insert,
			"Bad budget", date(t, "2026-06-01"), date(t, "2026-06-10"), 1, -1, "")
		assertCheckViolation(t, err)
	})

	t.Run("filter_mode must be '' or 'override'", func(t *testing.T) {
		_, err := s.db.ExecContext(ctx, insert,
			"Bad filter", date(t, "2026-06-01"), date(t, "2026-06-10"), 1, nil, "bogus")
		assertCheckViolation(t, err)
	})
}

func TestTripStayCheckConstraints(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	tripID, err := s.AddTrip(ctx, Trip{
		Name:      "Host trip",
		StartDate: date(t, "2026-06-01"),
		EndDate:   date(t, "2026-06-10"),
		MinNights: 1,
	})
	if err != nil {
		t.Fatalf("AddTrip: %v", err)
	}

	insert := `INSERT INTO trip_stay (trip_id, resort, room_type, check_in, check_out, nights, points) VALUES ($1, $2, $3, $4, $5, $6, $7)`

	t.Run("check_in must be before check_out", func(t *testing.T) {
		_, err := s.db.ExecContext(ctx, insert,
			tripID, "BLT", "Studio", date(t, "2026-06-10"), date(t, "2026-06-05"), 4, 100)
		assertCheckViolation(t, err)
	})

	t.Run("nights must equal check_out - check_in", func(t *testing.T) {
		_, err := s.db.ExecContext(ctx, insert,
			tripID, "BLT", "Studio", date(t, "2026-06-01"), date(t, "2026-06-05"), 99, 100)
		assertCheckViolation(t, err)
	})

	t.Run("points must be positive", func(t *testing.T) {
		_, err := s.db.ExecContext(ctx, insert,
			tripID, "BLT", "Studio", date(t, "2026-06-01"), date(t, "2026-06-05"), 4, 0)
		assertCheckViolation(t, err)
	})
}
