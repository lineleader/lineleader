-- name: InsertTrip :one
INSERT INTO trip (name, start_date, end_date, min_nights, budget_override, filter_mode, exclude_resorts, exclude_room_types)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING id;

-- name: ListTrips :many
SELECT id, name, start_date, end_date, min_nights, budget_override, filter_mode, exclude_resorts, exclude_room_types
FROM trip
ORDER BY start_date, id;

-- name: GetTrip :one
SELECT id, name, start_date, end_date, min_nights, budget_override, filter_mode, exclude_resorts, exclude_room_types
FROM trip
WHERE id = $1;

-- name: UpdateTrip :exec
UPDATE trip
SET name = $1, start_date = $2, end_date = $3, min_nights = $4, budget_override = $5, filter_mode = $6, exclude_resorts = $7, exclude_room_types = $8
WHERE id = $9;

-- name: InsertTripStay :one
INSERT INTO trip_stay (trip_id, resort, room_type, view, check_in, check_out, nights, points, quote_hash)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING id;

-- name: ListTripStays :many
SELECT id, trip_id, resort, room_type, view, check_in, check_out, nights, points, quote_hash
FROM trip_stay
WHERE trip_id = $1
ORDER BY check_in, id;

-- name: DeleteTrip :exec
DELETE FROM trip WHERE id = $1;

-- name: DeleteTripStay :exec
DELETE FROM trip_stay WHERE id = $1;

-- name: InsertTripStayEntry :exec
INSERT INTO trip_stay_entry (trip_stay_id, entry_id)
VALUES ($1, $2);

-- name: ListTripStayEntryIDsForTrip :many
-- Every (trip_stay_id, entry_id) link for tripID's stays, in one query —
-- Store.ListStays groups these by trip_stay_id in Go rather than each stay
-- issuing its own lookup.
SELECT tse.trip_stay_id, tse.entry_id
FROM trip_stay_entry tse
JOIN trip_stay ts ON ts.id = tse.trip_stay_id
WHERE ts.trip_id = $1
ORDER BY tse.trip_stay_id, tse.entry_id;
