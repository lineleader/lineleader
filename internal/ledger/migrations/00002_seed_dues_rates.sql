-- +goose Up
-- +goose StatementBegin

-- Reference data, seeded once by this migration rather than re-applied on
-- every boot. This keeps the whole-table WHERE NOT EXISTS guard that
-- seed.sql used under the old re-exec scheme (rather than switching to a
-- per-row ON CONFLICT) purely as a belt-and-suspenders measure: goose
-- itself already guarantees this file runs at most once via
-- goose_db_version, so in the steady state the guard is redundant. It
-- earns its keep only in the one scenario migrate_test.go rehearses —
-- TestOpen_BaselineOverPreExistingDatabase — where this migration runs
-- against percival's real database for the first time, and dues_rates
-- already has its 8 rows in it from years of seed.sql re-running on every
-- boot. Without the guard this INSERT would duplicate every row (dues_rates
-- has no unique constraint stopping that) instead of being a no-op.
--
-- Deliberate behaviour change from seed.sql: under the old scheme this
-- statement re-ran on every process start, so deleting all rows from
-- dues_rates through the UI got them resurrected on the next restart —
-- directly contradicting seed.sql's own comment, which says an operator
-- who deletes a seeded dues year must see it stay deleted. As a migration
-- this runs exactly once, ever (per goose_db_version), so a full deletion
-- now actually sticks across restarts. That is not a regression to guard
-- against; it is this migration finally delivering the behaviour seed.sql
-- always claimed to have. See TestOpen_SeededDuesSurviveDeletion.
INSERT INTO dues_rates (use_year, rate_micros)
SELECT * FROM (VALUES
    (2019, 6385000),
    (2020, 6561600),
    (2021, 6811800),
    (2022, 7007700),
    (2023, 7333200),
    (2024, 7574000),
    (2025, 7929800),
    (2026, 8223500)
) AS seeded_dues_rates(use_year, rate_micros)
WHERE NOT EXISTS (SELECT 1 FROM dues_rates);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM dues_rates;
-- +goose StatementEnd
