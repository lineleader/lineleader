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
INSERT INTO trip_stay (trip_id, resort, room_type, view, check_in, check_out, nights, points, quote_hash, entry_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING id;

-- name: ListTripStays :many
SELECT id, trip_id, resort, room_type, view, check_in, check_out, nights, points, quote_hash, entry_id
FROM trip_stay
WHERE trip_id = $1
ORDER BY check_in, id;
