#!/usr/bin/env bash
# Rebuild indexes and refresh collation versions on the shared Postgres.
#
# Why: that instance's data directory was initialized under glibc 2.36 and the
# host now provides 2.41. When glibc's collation rules change, text indexes
# built under the old rules can be subtly wrong — range queries returning
# incomplete results, unique indexes failing to catch duplicates. Postgres
# detects the mismatch and warns, but will not fix it for you.
#
# Run it against the container's stdin (it needs to run *inside* the container,
# where psql and the socket live):
#
#   ssh percival 'sudo docker exec -i postgresql bash -s' < deploy/refresh-collations.sh
#
# HEADS UP: REINDEX takes an ACCESS EXCLUSIVE lock on each index's table, so
# apps using these databases (miniflux, wall-e, mealie, lineleader…) will block
# for the duration. These are small databases — expect seconds, not minutes —
# but run it at a quiet moment rather than mid-use.
#
# Safe to re-run: every operation is idempotent.
set -euo pipefail

PGUSER_NAME="${PGUSER_NAME:-casaos}"
# Set INCLUDE_TEMPLATE0=0 to skip the template0 section (see below).
INCLUDE_TEMPLATE0="${INCLUDE_TEMPLATE0:-1}"

psql_do() {
    local db="$1" sql="$2"
    psql -U "$PGUSER_NAME" -d "$db" -v ON_ERROR_STOP=1 -q -c "$sql"
}

echo "==> Databases needing attention (before):"
psql -U "$PGUSER_NAME" -d postgres -v ON_ERROR_STOP=1 -c "
    SELECT datname,
           datcollversion AS recorded,
           pg_database_collation_actual_version(oid) AS actual
    FROM pg_database
    WHERE datcollversion IS DISTINCT FROM pg_database_collation_actual_version(oid)
    ORDER BY datname;"

# template0 is excluded here because datallowconn = false; it is handled
# separately at the end. template1 IS included on purpose: new databases are
# cloned from it, so a stale template1 keeps minting stale databases.
mapfile -t databases < <(
    psql -U "$PGUSER_NAME" -d postgres -Atc \
        "SELECT datname FROM pg_database WHERE datallowconn ORDER BY datname"
)

for db in "${databases[@]}"; do
    echo "==> $db"

    # REINDEX SYSTEM is NOT redundant: since PostgreSQL 14, REINDEX DATABASE
    # skips system catalogs, and catalog indexes on text columns are affected
    # by the collation change too.
    echo "    reindexing system catalogs"
    psql_do "$db" "REINDEX SYSTEM \"$db\";"

    echo "    reindexing user indexes"
    psql_do "$db" "REINDEX DATABASE \"$db\";"

    # Non-default collations carry their own version stamps, independent of the
    # database-level one.
    echo "    refreshing per-collation versions"
    psql_do "$db" "
        DO \$\$
        DECLARE c record;
        BEGIN
            FOR c IN
                SELECT nspname, collname
                FROM pg_collation
                JOIN pg_namespace ON pg_namespace.oid = collnamespace
                WHERE collversion IS DISTINCT FROM pg_collation_actual_version(pg_collation.oid)
            LOOP
                EXECUTE format('ALTER COLLATION %I.%I REFRESH VERSION', c.nspname, c.collname);
            END LOOP;
        END
        \$\$;"

    # Only now that every index has been rebuilt is it honest to record that
    # this database matches the current collation version.
    echo "    refreshing database collation version"
    psql_do "$db" "ALTER DATABASE \"$db\" REFRESH COLLATION VERSION;"
done

if [[ "$INCLUDE_TEMPLATE0" == "1" ]]; then
    # template0 is deliberately unconnectable and is the pristine source for
    # CREATE DATABASE ... TEMPLATE template0. Its catalog indexes were built at
    # initdb time under the old glibc, so databases cloned from it inherit them.
    # Open it just long enough to rebuild, then close it again.
    echo "==> template0 (temporarily allowing connections)"
    psql_do postgres "UPDATE pg_database SET datallowconn = true WHERE datname = 'template0';"
    trap 'psql -U "$PGUSER_NAME" -d postgres -q -c "UPDATE pg_database SET datallowconn = false WHERE datname = '"'"'template0'"'"';" || true' EXIT

    psql_do template0 "REINDEX SYSTEM \"template0\";"
    psql_do template0 "ALTER DATABASE \"template0\" REFRESH COLLATION VERSION;"

    psql_do postgres "UPDATE pg_database SET datallowconn = false WHERE datname = 'template0';"
    trap - EXIT
    echo "    connections disallowed again"
fi

echo
echo "==> Remaining mismatches (should be empty):"
psql -U "$PGUSER_NAME" -d postgres -v ON_ERROR_STOP=1 -c "
    SELECT datname,
           datcollversion AS recorded,
           pg_database_collation_actual_version(oid) AS actual
    FROM pg_database
    WHERE datcollversion IS DISTINCT FROM pg_database_collation_actual_version(oid)
    ORDER BY datname;"
