-- +goose Up
-- +goose StatementBegin

-- trip_stay_entry replaces trip_stay.entry_id: instead of a single nullable
-- FK on trip_stay pointing at the one entry a booked stay produced, a stay
-- is now linked to its entries through this join table. A stay still gets
-- exactly one entry when booked (see BookTrip in trip_book.go) — nothing
-- about booking BEHAVIOUR changes in this migration — but the new shape
-- lets a later change (issue nyj.4, stay-level point allocation across
-- current/banked/borrowed points) write SEVERAL entries per stay without
-- another schema migration.
CREATE TABLE trip_stay_entry (
    id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    trip_stay_id BIGINT NOT NULL REFERENCES trip_stay(id) ON DELETE CASCADE,
    -- UNIQUE, not just indexed: today's one-entry-per-stay invariant still
    -- holds, and this keeps the same entry from ever being linked to two
    -- stays. ON DELETE CASCADE (not SET NULL — there is no column left to
    -- null out) is what preserves the self-healing property
    -- docs/plans/trips.md relies on: deleting an entry from /ledger removes
    -- its trip_stay_entry row automatically, with no application code
    -- involved, and the stay's booked-ness — derived from whether it has
    -- any linked entries, never stored (see TripStay.Booked) — reverts to
    -- "not booked" the instant that row disappears. The old entry_id
    -- column's ON DELETE SET NULL achieved the identical effect by nulling
    -- itself out; this is the same self-healing FK, just pointed at a
    -- join-table row instead of a column.
    entry_id     BIGINT NOT NULL UNIQUE REFERENCES entry(id) ON DELETE CASCADE
);

CREATE INDEX idx_trip_stay_entry_trip_stay ON trip_stay_entry(trip_stay_id);

-- Backfill BEFORE the DROP below: trips booked under the old single-column
-- shape must survive this migration with their booked-ness intact. Every
-- trip_stay row that already has a non-NULL entry_id gets exactly one
-- trip_stay_entry row carrying that same link; unbooked stays (entry_id IS
-- NULL) get none, which is the correct "no linked entries" representation
-- under the new shape.
INSERT INTO trip_stay_entry (trip_stay_id, entry_id)
SELECT id, entry_id FROM trip_stay WHERE entry_id IS NOT NULL;

-- Only safe to run after the backfill above has copied every live link into
-- trip_stay_entry — reordering these two statements would silently unbook
-- every previously-booked trip the moment this migration ran.
ALTER TABLE trip_stay DROP COLUMN entry_id;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Down recreates entry_id and repopulates it only for stays linked to
-- EXACTLY one entry — the only case the old single-column shape could ever
-- represent. A stay linked to more than one entry (only possible once
-- nyj.4 lands and actually writes several) CANNOT be losslessly restored to
-- a single entry_id, so Down deliberately leaves entry_id NULL for those
-- rows rather than guessing which linked entry to keep. Down is therefore a
-- genuine, lossy downgrade for any trip booked under the multi-entry shape
-- — not a lossless round trip the way 00003's rename Down is.
ALTER TABLE trip_stay ADD COLUMN entry_id BIGINT REFERENCES entry(id) ON DELETE SET NULL;

UPDATE trip_stay
SET entry_id = single.entry_id
FROM (
    SELECT trip_stay_id, min(entry_id) AS entry_id
    FROM trip_stay_entry
    GROUP BY trip_stay_id
    HAVING count(*) = 1
) AS single
WHERE trip_stay.id = single.trip_stay_id;

DROP TABLE trip_stay_entry;

-- +goose StatementEnd
