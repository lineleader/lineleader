-- name: ListDuesRates :many
SELECT use_year, rate_micros
FROM dues_rates
ORDER BY use_year;

-- name: UpsertDuesRate :exec
INSERT INTO dues_rates (use_year, rate_micros) VALUES ($1, $2)
ON CONFLICT (use_year) DO UPDATE SET rate_micros = EXCLUDED.rate_micros;

-- name: DeleteDuesRate :exec
DELETE FROM dues_rates WHERE use_year = $1;
