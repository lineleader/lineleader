-- name: InsertEntry :one
INSERT INTO entry (use_year, date, description, kind, allotted, used, tag, contract_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING id;

-- name: ListEntries :many
SELECT id, use_year, date, description, kind, allotted, used, tag, contract_id
FROM entry
ORDER BY date, id;

-- name: UpdateEntry :exec
UPDATE entry
SET use_year = $1, date = $2, description = $3, kind = $4, allotted = $5, used = $6, tag = $7, contract_id = $8
WHERE id = $9;

-- name: DeleteEntry :exec
DELETE FROM entry WHERE id = $1;

-- name: DeleteEntriesForTrip :exec
DELETE FROM entry
 WHERE id IN (SELECT entry_id FROM trip_stay WHERE trip_id = $1 AND entry_id IS NOT NULL);

-- name: DeleteEntriesForStay :exec
DELETE FROM entry
 WHERE id IN (SELECT entry_id FROM trip_stay WHERE trip_stay.id = $1 AND entry_id IS NOT NULL);
