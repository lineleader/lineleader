CREATE TABLE IF NOT EXISTS contracts (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    name           TEXT    NOT NULL,
    number         TEXT    NOT NULL DEFAULT '',
    home_resort    TEXT    NOT NULL DEFAULT '',
    annual_points  INTEGER NOT NULL DEFAULT 0,
    use_year_month INTEGER NOT NULL DEFAULT 1
);

CREATE TABLE IF NOT EXISTS entries (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    use_year    INTEGER NOT NULL,
    date        TEXT    NOT NULL,            -- RFC3339 date (YYYY-MM-DD)
    description TEXT    NOT NULL DEFAULT '',
    kind        TEXT    NOT NULL,
    allotted    INTEGER NOT NULL DEFAULT 0,
    used        INTEGER NOT NULL DEFAULT 0,
    tag         TEXT    NOT NULL DEFAULT '',
    contract_id INTEGER REFERENCES contracts(id) ON DELETE SET NULL
);

CREATE INDEX IF NOT EXISTS idx_entries_order ON entries(date, id);
CREATE INDEX IF NOT EXISTS idx_entries_use_year ON entries(use_year);
