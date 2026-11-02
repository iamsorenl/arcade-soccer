#!/usr/bin/env bash
# Loads a Supabase backup (from scripts/dump-supabase.sh) into the self-hosted
# League database, keeping every user id so teams/Elo/matches stay attached.
#
#   scripts/migrate-from-supabase.sh DUMP_DIR DST_URL [DAY]
#
# DUMP_DIR holds supabase-full-DAY.dump + auth-users-DAY.csv (DAY defaults to
# the newest pair). DST_URL must already have server/db/schema.sql applied and
# no users yet. Everything loads in one transaction: any error writes nothing.
# Needs Docker (pg_restore 17 reads the dump) and a local psql.
set -euo pipefail

DUMP_DIR=${1:?usage: migrate-from-supabase.sh DUMP_DIR DST_URL [DAY]}
DST_URL=${2:?usage: migrate-from-supabase.sh DUMP_DIR DST_URL [DAY]}
DUMP_DIR=$(cd "$DUMP_DIR" && pwd)
DAY=${3:-$(ls "$DUMP_DIR" | sed -n 's/^supabase-full-\(.*\)\.dump$/\1/p' | sort | tail -1)}
DUMP=supabase-full-$DAY.dump
CSV=$DUMP_DIR/auth-users-$DAY.csv
[ -f "$DUMP_DIR/$DUMP" ] && [ -f "$CSV" ] || { echo "missing $DUMP or $(basename "$CSV") in $DUMP_DIR" >&2; exit 1; }

q() { PGOPTIONS="-c client_min_messages=warning" psql "$DST_URL" -v ON_ERROR_STOP=1 -qAt "$@"; }

existing=$(q -c "select count(*) from users")
[ "$existing" = 0 ] || { echo "destination already has $existing users; refusing to load on top" >&2; exit 1; }

DATA=$(mktemp)
trap 'rm -f "$DATA"' EXIT
# One table per pg_restore call, parents first: a single multi-table restore
# emits matches before teams, which trips the foreign keys.
for t in profiles teams matches; do
  docker run --rm -v "$DUMP_DIR:/in:ro" postgres:17 \
    pg_restore --data-only --no-owner -t "$t" -f - "/in/$DUMP"
done | grep -vE '^\\(un)?restrict ' > "$DATA" # pg_dump 17.6+ meta-commands older psql rejects

# Rows per table in the dump: lines between "COPY public.<t>" and "\.".
src_count() { awk -v t="COPY public.$1 " 'index($0,t)==1{on=1;next} on&&$0=="\\."{on=0} on{n++} END{print n+0}' "$DATA"; }

{
  echo "begin;"
  echo "create temp table src_users (id uuid, email text, created_at timestamptz);"
  echo "\\copy src_users from '$CSV' csv header"
  # Stop before writing anything if two accounts only differ by case/spacing:
  # after normalization they would collide, and picking one silently would
  # strand the other's team.
  cat <<'SQL'
do $$
declare dups text;
begin
  select string_agg(e, ', ') into dups from (
    select lower(btrim(email)) e from src_users group by 1 having count(*) > 1
  ) d;
  if dups is not null then
    raise exception 'emails that collide after lowercasing: %', dups;
  end if;
end $$;
insert into users (id, email, created_at)
  select id, lower(btrim(email)), created_at from src_users;
SQL
  cat "$DATA"
  echo "commit;"
} | q

printf '%-9s %6s %6s\n' table source loaded
fail=0
for t in users profiles teams matches; do
  if [ "$t" = users ]; then s=$(($(wc -l < "$CSV") - 1)); else s=$(src_count "$t"); fi
  d=$(q -c "select count(*) from public.$t")
  printf '%-9s %6s %6s\n' "$t" "$s" "$d"
  [ "$s" = "$d" ] || fail=1
done
[ "$fail" = 0 ] || { echo "row counts differ" >&2; exit 1; }
echo "migration ok"
