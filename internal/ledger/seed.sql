-- Reference data seeded on every process start (internal/ledger/store.go's
-- Open, after schema.sql). Unlike schema.sql this is DML, not DDL — it must
-- stay idempotent under a *table-level* guard, not a per-row ON CONFLICT:
-- an operator who deletes a seeded dues year through the UI must see it
-- stay deleted across a restart, not get resurrected here.
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
