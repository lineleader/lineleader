-- name: InsertContract :one
INSERT INTO contracts (name, number, home_resort, annual_points, use_year_month, term_years, purchase_price_cents, closing_costs_cents)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING id;

-- name: ListContracts :many
SELECT id, name, number, home_resort, annual_points, use_year_month, term_years, purchase_price_cents, closing_costs_cents
FROM contracts
ORDER BY id;

-- name: UpdateContract :exec
UPDATE contracts
SET name = $1, number = $2, home_resort = $3, annual_points = $4, use_year_month = $5,
    term_years = $6, purchase_price_cents = $7, closing_costs_cents = $8
WHERE id = $9;

-- name: DeleteContract :exec
DELETE FROM contracts WHERE id = $1;
