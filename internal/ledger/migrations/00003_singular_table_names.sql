-- +goose Up
-- +goose StatementBegin

-- Renames the three ledger tables from plural to singular, establishing the
-- convention every table added after this one follows: a table is named for
-- the single row it holds (contract, entry, dues_rate), not the collection.
-- The Trips tables land as `trip` and `trip_stay` for the same reason.
--
-- This is a pure rename: no column changes, no data movement, no rewrite.
-- Postgres rewrites the catalog entry and every row stays exactly where it
-- is, so this is fast and safe even against percival's populated ledger.
--
-- Renaming a table does NOT rename the objects hanging off it — indexes,
-- constraints and identity sequences all keep the name they were born with.
-- Left alone, percival would end up with a table `contract` whose primary
-- key is still called `contracts_pkey`, which is exactly the kind of
-- half-applied convention that makes a later reader distrust the schema.
-- So each one is renamed explicitly below. The identity sequences are
-- renamed for the same reason, though nothing reads their names directly:
-- cmd/ledger-migrate resolves them through pg_get_serial_sequence() rather
-- than spelling them out.
ALTER TABLE contracts  RENAME TO contract;
ALTER TABLE entries    RENAME TO entry;
ALTER TABLE dues_rates RENAME TO dues_rate;

ALTER INDEX contracts_pkey       RENAME TO contract_pkey;
ALTER INDEX entries_pkey         RENAME TO entry_pkey;
ALTER INDEX dues_rates_pkey      RENAME TO dues_rate_pkey;
ALTER INDEX idx_entries_order    RENAME TO idx_entry_order;
ALTER INDEX idx_entries_use_year RENAME TO idx_entry_use_year;

ALTER TABLE entry RENAME CONSTRAINT entries_contract_id_fkey TO entry_contract_id_fkey;

ALTER SEQUENCE contracts_id_seq RENAME TO contract_id_seq;
ALTER SEQUENCE entries_id_seq   RENAME TO entry_id_seq;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Unlike the baseline's deliberately-inert Down, this one is real and safe
-- to run: a rename is losslessly reversible, so `goose down` restores the
-- plural names exactly rather than destroying anything.
ALTER SEQUENCE entry_id_seq    RENAME TO entries_id_seq;
ALTER SEQUENCE contract_id_seq RENAME TO contracts_id_seq;

ALTER TABLE entry RENAME CONSTRAINT entry_contract_id_fkey TO entries_contract_id_fkey;

ALTER INDEX idx_entry_use_year RENAME TO idx_entries_use_year;
ALTER INDEX idx_entry_order    RENAME TO idx_entries_order;
ALTER INDEX dues_rate_pkey     RENAME TO dues_rates_pkey;
ALTER INDEX entry_pkey         RENAME TO entries_pkey;
ALTER INDEX contract_pkey      RENAME TO contracts_pkey;

ALTER TABLE dues_rate RENAME TO dues_rates;
ALTER TABLE entry     RENAME TO entries;
ALTER TABLE contract  RENAME TO contracts;

-- +goose StatementEnd
