#!/usr/bin/env bash
# Local League stack for manual testing: sim (8081) + Go API (8080) on the
# league_dev database in the `league-pg` container (postgres:17, 127.0.0.1:55432).
#
#   scripts/dev-league.sh                 # start with whatever is in league_dev
#   scripts/dev-league.sh --seed DIR      # wipe league_dev, load the Supabase dump in DIR first
#
# Ctrl-C stops both. Sign-in links print in this log (no SMTP_HOST).
# API_PORT / SIM_PORT override the ports (default 8080 / 8081).
set -euo pipefail

cd "$(dirname "$0")/.."

SEED_DIR=
case "${1:-}" in
  "") ;;
  --seed) SEED_DIR=${2:?usage: dev-league.sh [--seed DUMP_DIR]} ;;
  *) echo "usage: dev-league.sh [--seed DUMP_DIR]" >&2; exit 2 ;;
esac

API_PORT=${API_PORT:-8080}
SIM_PORT=${SIM_PORT:-8081}
DB_URL=postgres://postgres:dev@127.0.0.1:55432/league_dev
psql_pg() { docker exec -i league-pg psql -U postgres -v ON_ERROR_STOP=1 -qAt "$@"; }

docker exec league-pg pg_isready -U postgres -q || { echo "container league-pg is not running" >&2; exit 1; }

if [ "$(psql_pg -d postgres -c "select 1 from pg_database where datname='league_dev'")" != 1 ]; then
  psql_pg -d postgres -c "create database league_dev"
fi

if [ -n "$SEED_DIR" ]; then
  psql_pg -d league_dev -c "drop schema public cascade; create schema public" 2>/dev/null
  psql_pg -d league_dev < server/db/schema.sql
  scripts/migrate-from-supabase.sh "$SEED_DIR" "$DB_URL"
fi

BIN=$(mktemp -d)
pids=()
cleanup() {
  trap - INT TERM EXIT
  [ "${#pids[@]}" -eq 0 ] || kill "${pids[@]}" 2>/dev/null || true
  wait 2>/dev/null || true
  rm -rf "$BIN"
}
trap cleanup INT TERM EXIT

(cd server && go build -o "$BIN/api" ./api)

PORT=$SIM_PORT node server/sim/sim.mjs &
pids+=($!)
DATABASE_URL=$DB_URL PORT=$API_PORT SIM_URL=http://127.0.0.1:$SIM_PORT \
  APP_URL=http://localhost:8000/ ALLOWED_ORIGINS=http://localhost:8000 \
  "$BIN/api" &
pids+=($!)

cat <<MSG

League dev stack: API http://localhost:$API_PORT, sim http://localhost:$SIM_PORT (db league_dev)
To use it:
  1. set LEAGUE_API_URL in js/league-config.js to 'http://localhost:$API_PORT' (local only, don't commit)
  2. in another terminal, from the repo root: python3 -m http.server 8000
  3. open http://localhost:8000/
Sign-in links show up below. Ctrl-C stops everything.

MSG
wait
