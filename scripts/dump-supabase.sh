#!/usr/bin/env bash
# Full backup of the Supabase League database, plus auth.users (id, email) as CSV
# for the self-host migration. Uses Docker's pg_dump 17 because Supabase runs
# Postgres 17 and pg_dump refuses to dump a newer server.
#
# Usage: SUPABASE_DB_URL=postgres://... scripts/dump-supabase.sh OUT_DIR
#   (or put SUPABASE_DB_URL in the gitignored .env at the repo root)
set -euo pipefail

OUT_DIR=${1:?usage: dump-supabase.sh OUT_DIR}
cd "$(dirname "$0")/.."
if [ -z "${SUPABASE_DB_URL:-}" ] && [ -f .env ]; then set -a; . ./.env; set +a; fi
: "${SUPABASE_DB_URL:?set SUPABASE_DB_URL (Supabase dashboard > Connect > Session pooler)}"

mkdir -p "$OUT_DIR"
OUT_DIR=$(cd "$OUT_DIR" && pwd)
DAY=$(date +%F)
run() { docker run --rm -e SRC="$SUPABASE_DB_URL" -v "$OUT_DIR:/out" postgres:17 bash -c "$1"; }

run "pg_dump \"\$SRC\" -Fc -f /out/supabase-full-$DAY.dump"
run "psql \"\$SRC\" -v ON_ERROR_STOP=1 -c \"\\copy (select id, email, created_at from auth.users order by created_at) to '/out/auth-users-$DAY.csv' csv header\""

# Sanity: the dump has the League tables, the CSV has rows.
run "pg_restore --list /out/supabase-full-$DAY.dump" | grep -E 'TABLE DATA public (profiles|teams|matches) '
echo "auth users: $(($(wc -l < "$OUT_DIR/auth-users-$DAY.csv") - 1))"
ls -lh "$OUT_DIR"/*-"$DAY".*
