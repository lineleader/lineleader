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

-- Columns added after the initial release. Nothing below this comment may
-- ever be removed: schema.sql is re-executed as a single multi-statement
-- Exec on every process start against the one deployed database, and there
-- is no migration tool — a column change must be expressed both in the
-- CREATE TABLE above (for a fresh database) and here (for the deployed
-- one), and every statement here must stay idempotent forever.
ALTER TABLE contracts ADD COLUMN IF NOT EXISTS term_years           INTEGER NOT NULL DEFAULT 0;
ALTER TABLE contracts ADD COLUMN IF NOT EXISTS purchase_price_cents BIGINT  NOT NULL DEFAULT 0;
ALTER TABLE contracts ADD COLUMN IF NOT EXISTS closing_costs_cents  BIGINT  NOT NULL DEFAULT 0;
