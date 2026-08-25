-- name: UseYearSummaries :many
SELECT use_year, COALESCE(SUM(allotted), 0)::bigint AS allotted, COALESCE(SUM(used), 0)::bigint AS used
FROM entry
GROUP BY use_year
ORDER BY use_year;

-- name: LatestAllocationYear :one
SELECT use_year FROM entry
WHERE contract_id = $1 AND kind = $2
ORDER BY use_year DESC LIMIT 1;

-- name: CountAllocationFor :one
SELECT COUNT(1) FROM entry
WHERE contract_id = $1 AND kind = $2 AND use_year = $3;
