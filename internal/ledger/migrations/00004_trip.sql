-- +goose Up
-- +goose StatementBegin

-- Trip is the persisted replacement for the old ~/.config/lineleader/plans.json
-- Plan: a named date window the user is planning a vacation around. See
-- docs/plans (trips design) section 1 for the full rationale behind every
-- column and every decision NOT made here (no stored status, no
-- resort_code, no created_at).
--
-- Table names are singular, per the convention 00003_singular_table_names.sql
-- established: trip / trip_stay, not trips / trip_stays.
CREATE TABLE trip (
    id                 BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name               TEXT    NOT NULL,
    start_date         DATE    NOT NULL,
    end_date           DATE    NOT NULL,
    min_nights         INTEGER NOT NULL DEFAULT 1,
    budget_override    INTEGER,                     -- NULL = use the computed budget
    filter_mode        TEXT    NOT NULL DEFAULT '', -- '' inherit | 'override'
    exclude_resorts    TEXT    NOT NULL DEFAULT '[]',
    exclude_room_types TEXT    NOT NULL DEFAULT '[]',
    CHECK (start_date < end_date),
    CHECK (min_nights BETWEEN 1 AND 30),            -- mirrors dvc.MaxNights
    CHECK (budget_override IS NULL OR budget_override >= 0),
    CHECK (filter_mode IN ('', 'override'))
);

-- TripStay is a lossless serialization of one dvc.StayResult a trip has
-- collected, plus the ledger entry it produced once booked (entry_id NULL =
-- not booked). Booked-ness is derived from entry_id, never stored — see the
-- plan for why a stored status would go stale the moment an entry is
-- deleted from /ledger.
CREATE TABLE trip_stay (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    trip_id    BIGINT  NOT NULL REFERENCES trip(id) ON DELETE CASCADE,
    resort     TEXT    NOT NULL,          -- the resort NAME, as dvc.StayResult carries it
    room_type  TEXT    NOT NULL,
    view       TEXT    NOT NULL DEFAULT '',
    check_in   DATE    NOT NULL,
    check_out  DATE    NOT NULL,
    nights     INTEGER NOT NULL,
    points     INTEGER NOT NULL,
    quote_hash TEXT    NOT NULL DEFAULT '',
    entry_id   BIGINT REFERENCES entry(id) ON DELETE SET NULL,
    CHECK (check_in < check_out),
    CHECK (nights = check_out - check_in),
    CHECK (points > 0)
);

CREATE INDEX idx_trip_stay_trip ON trip_stay(trip_id, check_in, id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- trip_stay references trip, so it must drop first to satisfy the FK.
DROP TABLE trip_stay;
DROP TABLE trip;

-- +goose StatementEnd
