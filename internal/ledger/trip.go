package ledger

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/lineleader/lineleader/internal/ledger/dbgen"
)

// Trip filter modes. Global filter defaults live in config.json; a trip
// either inherits them (TripFilterInherit, the default) or overrides them
// with its own ExcludeResorts/ExcludeRoomTypes.
const (
	TripFilterInherit  = ""
	TripFilterOverride = "override"
)

// ErrTripNotFound is returned by GetTrip when id does not match any trip.
// Callers distinguish "no such trip" from a lower-level query failure with
// errors.Is(err, ErrTripNotFound).
var ErrTripNotFound = errors.New("trip not found")

// Trip is a persisted, named date window the user is planning a vacation
// around — the replacement for the old ~/.config/lineleader/plans.json
// Plan. It deliberately stores no status and no computed budget: see the
// trips design doc section 1 for why both are derived rather than stored.
type Trip struct {
	ID               int64
	Name             string
	StartDate        time.Time
	EndDate          time.Time
	MinNights        int
	BudgetOverride   *int   // nil = use the computed budget
	FilterMode       string // TripFilterInherit | TripFilterOverride
	ExcludeResorts   []string
	ExcludeRoomTypes []string
}

// TripStay is a lossless serialization of one dvc.StayResult a trip has
// collected. internal/ledger must not import internal/dvc (that would
// invert the layering), so Resort/RoomType/View are plain strings rather
// than dvc's own types. EntryID is nil until the stay is booked; booked-ness
// is always derived from it, never stored separately.
type TripStay struct {
	ID        int64
	TripID    int64
	Resort    string // the resort NAME, as dvc.StayResult carries it
	RoomType  string
	View      string
	CheckIn   time.Time
	CheckOut  time.Time
	Nights    int
	Points    int
	QuoteHash string
	EntryID   *int64 // nil = not booked
}

// nullInt32FromIntPtr converts a possibly-nil *int into the sql.NullInt32
// dbgen's generated params expect for the nullable trip.budget_override
// column.
func nullInt32FromIntPtr(v *int) sql.NullInt32 {
	if v == nil {
		return sql.NullInt32{}
	}
	return sql.NullInt32{Int32: int32(*v), Valid: true}
}

// intPtrFromNullInt32 is the inverse of nullInt32FromIntPtr.
func intPtrFromNullInt32(v sql.NullInt32) *int {
	if !v.Valid {
		return nil
	}
	i := int(v.Int32)
	return &i
}

// nullInt64FromPtr converts a possibly-nil *int64 into the sql.NullInt64
// dbgen's generated params expect for the nullable trip_stay.entry_id
// column.
func nullInt64FromPtr(v *int64) sql.NullInt64 {
	if v == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *v, Valid: true}
}

// ptrFromNullInt64 is the inverse of nullInt64FromPtr.
func ptrFromNullInt64(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	id := v.Int64
	return &id
}

// marshalStringList encodes list as the JSON array text stored in
// exclude_resorts/exclude_room_types. A nil list marshals to "[]", matching
// the columns' schema default so AddTrip never writes NULL.
func marshalStringList(list []string) (string, error) {
	if list == nil {
		list = []string{}
	}
	b, err := json.Marshal(list)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// unmarshalStringList decodes a exclude_resorts/exclude_room_types column
// value. An empty JSON array reads back as a nil slice, matching this
// package's existing nil-slice-when-empty convention (see ListEntries).
func unmarshalStringList(s string) ([]string, error) {
	var list []string
	if err := json.Unmarshal([]byte(s), &list); err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, nil
	}
	return list, nil
}

// tripFromRow maps a dbgen.Trip (sqlc's generated model) onto the domain
// Trip type, decoding the JSON filter columns.
func tripFromRow(row dbgen.Trip) (Trip, error) {
	excludeResorts, err := unmarshalStringList(row.ExcludeResorts)
	if err != nil {
		return Trip{}, fmt.Errorf("trip %d: decoding exclude_resorts: %w", row.ID, err)
	}
	excludeRoomTypes, err := unmarshalStringList(row.ExcludeRoomTypes)
	if err != nil {
		return Trip{}, fmt.Errorf("trip %d: decoding exclude_room_types: %w", row.ID, err)
	}
	return Trip{
		ID:               row.ID,
		Name:             row.Name,
		StartDate:        row.StartDate,
		EndDate:          row.EndDate,
		MinNights:        int(row.MinNights),
		BudgetOverride:   intPtrFromNullInt32(row.BudgetOverride),
		FilterMode:       row.FilterMode,
		ExcludeResorts:   excludeResorts,
		ExcludeRoomTypes: excludeRoomTypes,
	}, nil
}

// tripStayFromRow maps a dbgen.TripStay (sqlc's generated model) onto the
// domain TripStay type.
func tripStayFromRow(row dbgen.TripStay) TripStay {
	return TripStay{
		ID:        row.ID,
		TripID:    row.TripID,
		Resort:    row.Resort,
		RoomType:  row.RoomType,
		View:      row.View,
		CheckIn:   row.CheckIn,
		CheckOut:  row.CheckOut,
		Nights:    int(row.Nights),
		Points:    int(row.Points),
		QuoteHash: row.QuoteHash,
		EntryID:   ptrFromNullInt64(row.EntryID),
	}
}

// AddTrip inserts t and returns its new id.
func (s *Store) AddTrip(ctx context.Context, t Trip) (int64, error) {
	excludeResorts, err := marshalStringList(t.ExcludeResorts)
	if err != nil {
		return 0, err
	}
	excludeRoomTypes, err := marshalStringList(t.ExcludeRoomTypes)
	if err != nil {
		return 0, err
	}
	return s.q.InsertTrip(ctx, dbgen.InsertTripParams{
		Name:             t.Name,
		StartDate:        t.StartDate,
		EndDate:          t.EndDate,
		MinNights:        int32(t.MinNights),
		BudgetOverride:   nullInt32FromIntPtr(t.BudgetOverride),
		FilterMode:       t.FilterMode,
		ExcludeResorts:   excludeResorts,
		ExcludeRoomTypes: excludeRoomTypes,
	})
}

// ListTrips returns every trip ordered by (start_date, id).
func (s *Store) ListTrips(ctx context.Context) ([]Trip, error) {
	rows, err := s.q.ListTrips(ctx)
	if err != nil {
		return nil, err
	}
	var out []Trip
	for _, row := range rows {
		t, err := tripFromRow(row)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

// GetTrip returns the trip identified by id, or ErrTripNotFound if no such
// trip exists.
func (s *Store) GetTrip(ctx context.Context, id int64) (Trip, error) {
	row, err := s.q.GetTrip(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Trip{}, ErrTripNotFound
	}
	if err != nil {
		return Trip{}, err
	}
	return tripFromRow(row)
}

// UpdateTrip overwrites the trip identified by t.ID.
func (s *Store) UpdateTrip(ctx context.Context, t Trip) error {
	excludeResorts, err := marshalStringList(t.ExcludeResorts)
	if err != nil {
		return err
	}
	excludeRoomTypes, err := marshalStringList(t.ExcludeRoomTypes)
	if err != nil {
		return err
	}
	return s.q.UpdateTrip(ctx, dbgen.UpdateTripParams{
		Name:             t.Name,
		StartDate:        t.StartDate,
		EndDate:          t.EndDate,
		MinNights:        int32(t.MinNights),
		BudgetOverride:   nullInt32FromIntPtr(t.BudgetOverride),
		FilterMode:       t.FilterMode,
		ExcludeResorts:   excludeResorts,
		ExcludeRoomTypes: excludeRoomTypes,
		ID:               t.ID,
	})
}

// AddStay inserts st and returns its new id.
func (s *Store) AddStay(ctx context.Context, st TripStay) (int64, error) {
	return s.q.InsertTripStay(ctx, dbgen.InsertTripStayParams{
		TripID:    st.TripID,
		Resort:    st.Resort,
		RoomType:  st.RoomType,
		View:      st.View,
		CheckIn:   st.CheckIn,
		CheckOut:  st.CheckOut,
		Nights:    int32(st.Nights),
		Points:    int32(st.Points),
		QuoteHash: st.QuoteHash,
		EntryID:   nullInt64FromPtr(st.EntryID),
	})
}

// ListStays returns every stay belonging to tripID, ordered by
// (check_in, id).
func (s *Store) ListStays(ctx context.Context, tripID int64) ([]TripStay, error) {
	rows, err := s.q.ListTripStays(ctx, tripID)
	if err != nil {
		return nil, err
	}
	var out []TripStay
	for _, row := range rows {
		out = append(out, tripStayFromRow(row))
	}
	return out, nil
}

// QuoteHash fingerprints the chart inputs that produced a stay's point
// quote. Nothing reads it yet — see plan §1 for why it is stored now
// rather than backfilled later.
//
// Inputs are joined with a unit-separator (0x1F) byte before hashing so
// the encoding is unambiguous: without a delimiter, nightlyPoints [1, 23]
// and [12, 3] would concatenate to the same digit stream. Prefixing every
// field (including each element of nightlyPoints) with its own separator
// keeps regroupings like that one distinct.
func QuoteHash(resortCode string, year, columnIndex int, nightlyPoints []int) string {
	const sep = "\x1f"
	h := sha256.New()
	fmt.Fprintf(h, "%s%s%d%s%d", resortCode, sep, year, sep, columnIndex)
	for _, p := range nightlyPoints {
		fmt.Fprintf(h, "%s%d", sep, p)
	}
	return hex.EncodeToString(h.Sum(nil))
}
