-- +goose Up
-- +goose StatementBegin

-- This is the baseline migration: it describes the schema exactly as it
-- existed on the percival deployment the day this repo switched from
-- re-executing schema.sql on every boot to goose. The three
-- `ALTER TABLE contracts ADD COLUMN IF NOT EXISTS ...` statements that used
-- to trail schema.sql (added after the initial release, applied a second
-- time on top of the CREATE TABLE for the already-deployed database) are
-- folded straight into the contracts CREATE TABLE below — there is no
-- longer a "fresh database" shape and a "deployed database" shape to keep
-- in sync, because goose_db_version now records that this migration ran
-- and goose will never re-run it.
--
-- Every CREATE TABLE / CREATE INDEX below keeps its IF NOT EXISTS guard on
-- purpose, even though a normal goose migration wouldn't need one (goose
-- itself guarantees this file runs at most once via goose_db_version).
-- The guards are load-bearing here for a different reason: this migration
-- also has to succeed the *first* time it runs against percival's real,
-- already-populated database, which has these exact tables and indexes
-- already sitting in it from years of the old re-exec scheme, but has
-- never had a goose_db_version table before now. Dropping IF NOT EXISTS
-- would make that first run fail with "relation already exists" and take
-- down the one production database this whole migration system exists to
-- protect. See migrate_test.go's TestOpen_BaselineOverPreExistingDatabase,
-- which rehearses exactly this scenario against a frozen copy of that
-- database (internal/ledger/testdata/legacy_schema.sql).
CREATE TABLE IF NOT EXISTS contracts (
    id                   BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name                 TEXT    NOT NULL,
    number               TEXT    NOT NULL DEFAULT '',
    home_resort          TEXT    NOT NULL DEFAULT '',
    annual_points        INTEGER NOT NULL DEFAULT 0,
    use_year_month       INTEGER NOT NULL DEFAULT 1,
    term_years           INTEGER NOT NULL DEFAULT 0,
    purchase_price_cents BIGINT  NOT NULL DEFAULT 0,
    closing_costs_cents  BIGINT  NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS entries (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    use_year    INTEGER NOT NULL,
    date        TEXT    NOT NULL,            -- ISO date (YYYY-MM-DD)
    description TEXT    NOT NULL DEFAULT '',
    kind        TEXT    NOT NULL,
    allotted    INTEGER NOT NULL DEFAULT 0,
    used        INTEGER NOT NULL DEFAULT 0,
    tag         TEXT    NOT NULL DEFAULT '',
    contract_id BIGINT REFERENCES contracts(id) ON DELETE SET NULL
);

CREATE INDEX IF NOT EXISTS idx_entries_order ON entries(date, id);
CREATE INDEX IF NOT EXISTS idx_entries_use_year ON entries(use_year);

CREATE TABLE IF NOT EXISTS dues_rates (
    use_year    INTEGER PRIMARY KEY,
    rate_micros BIGINT  NOT NULL
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Deliberately a documented no-op, not a DROP TABLE. Every migration after
-- this one gets a real, reversible Down block — but this one is a baseline
-- laid on top of a pre-existing production database (percival), not a
-- migration that created fresh tables goose owns end to end. A `goose down`
-- that reached this file would drop contracts, entries, and dues_rates —
-- i.e. destroy the entire real ledger — which is never the right response
-- to "undo the last migration" for a baseline. If this schema ever needs to
-- be undone, that is a deliberate, manual, backed-up operation, not
-- something `goose down` should be able to trigger by walking off the end
-- of the migration list.
SELECT 1;
-- +goose StatementEnd
