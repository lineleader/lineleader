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
// than dvc's own types. EntryIDs lists the ledger entries this stay is
// linked to via the trip_stay_entry table — empty until the stay is
// booked. Booked-ness is always derived from it (see TripStay.Booked),
// never stored separately. BookTrip links exactly one entry per stay today
// (see trip_book.go); the slice shape exists so a later change (issue
// nyj.4, stay-level point allocation) can link several without another
// schema change.
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
	EntryIDs  []int64 // empty = not booked

	// EntryPoints is the sum of Used across every entry in EntryIDs — 0
	// when the stay is unbooked. It exists so Booked() alone (which only
	// checks whether EntryIDs is non-empty) doesn't have to be trusted for
	// "fully funded": someone deleting one of a stay's linked entries on
	// /ledger leaves EntryIDs non-empty but EntryPoints short of Points.
	// See PartiallyBooked.
	EntryPoints int
}

// Booked reports whether st has at least one linked ledger entry. This is
// the single place "booked" is derived from EntryIDs — callers must never
// re-derive it by checking len(EntryIDs) themselves, the same discipline
// the trips design doc asks of the web layer's own status derivation.
func (st TripStay) Booked() bool {
	return len(st.EntryIDs) > 0
}

// PartiallyBooked reports whether st has linked entries whose points fall
// short of the stay's own Points — e.g. someone deleted one of the entries
// a multi-lot booking created for this stay on /ledger, without removing
// the trip_stay_entry link to the rest. Booked() alone can't see this: it
// only checks whether EntryIDs is non-empty, so a partially-booked stay
// reports Booked() == true even though it isn't fully funded.
func (st TripStay) PartiallyBooked() bool {
	return st.Booked() && st.EntryPoints < st.Points
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
// domain TripStay type. EntryIDs is left at its zero value — trip_stay no
// longer carries its own links, so ListStays fills EntryIDs in separately
// from a ListTripStayEntryIDsForTrip lookup.
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

// AddStay inserts st and returns its new id. A stay is always inserted
// unbooked — st.EntryIDs is ignored; a stay only gains links via BookTrip
// (or a later nyj.4 allocator), inserted into trip_stay_entry, never on the
// trip_stay row itself.
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
	})
}

// ListStays returns every stay belonging to tripID, ordered by
// (check_in, id), with each stay's EntryIDs populated from
// trip_stay_entry. Two queries, not a join: trip_stay_entry can hold
// several rows per stay, and grouping that in SQL (array_agg) would need a
// Postgres array type on the Go side purely for this one field, where a
// second flat query plus an in-memory group-by keeps every other query in
// this package's plain scalar-per-column shape.
func (s *Store) ListStays(ctx context.Context, tripID int64) ([]TripStay, error) {
	rows, err := s.q.ListTripStays(ctx, tripID)
	if err != nil {
		return nil, err
	}
	links, err := s.q.ListTripStayEntryIDsForTrip(ctx, tripID)
	if err != nil {
		return nil, err
	}
	entryIDsByStay := make(map[int64][]int64, len(links))
	for _, l := range links {
		entryIDsByStay[l.TripStayID] = append(entryIDsByStay[l.TripStayID], l.EntryID)
	}

	used, err := s.q.ListTripStayUsedPointsForTrip(ctx, tripID)
	if err != nil {
		return nil, err
	}
	usedByStay := make(map[int64]int, len(used))
	for _, u := range used {
		usedByStay[u.TripStayID] = int(u.Used)
	}

	var out []TripStay
	for _, row := range rows {
		st := tripStayFromRow(row)
		st.EntryIDs = entryIDsByStay[st.ID]
		st.EntryPoints = usedByStay[st.ID]
		out = append(out, st)
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
