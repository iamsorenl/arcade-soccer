#!/usr/bin/env bash
# Checks server/db/schema.sql: applies twice (idempotent), the hourly cap
# raises PT429 on the 11th match, W/D/L add up, deleting a user cascades.
# WIPES schema public in TEST_DATABASE_URL — point it at a throwaway database.
#
# Usage: TEST_DATABASE_URL=postgres://localhost/league_test bash server/db/schema_test.sh
set -euo pipefail
: "${TEST_DATABASE_URL:?set TEST_DATABASE_URL to a throwaway database}"
cd "$(dirname "$0")"
q() { PGOPTIONS="-c client_min_messages=warning" psql "$TEST_DATABASE_URL" -v ON_ERROR_STOP=1 -qAt "$@"; }

q -c 'drop schema if exists public cascade; create schema public;'
q -f schema.sql
q -f schema.sql # second apply must be a no-op

A=00000000-0000-0000-0000-00000000000a
B=00000000-0000-0000-0000-00000000000b
q <<SQL
insert into users (id, email) values ('$A', 'a@example.com'), ('$B', 'b@example.com');
insert into profiles (id, username) values ('$A', 'alice'), ('$B', 'bobby');
insert into teams (id, owner, name, config) values
  ('$A', '$A', 'A team', '{}'), ('$B', '$B', 'B team', '{}');
SQL

fail() { echo "FAIL: $*" >&2; exit 1; }
match() { # score_a score_b
  q -c "select apply_match_result('$A','$B',1,1,1,$1,$2,16,-16,1,'{}','{}',10)"
}

for i in $(seq 1 10); do match 2 1 >/dev/null; done
err=$(match 2 1 2>&1 >/dev/null || true)
case "$err" in *"rate limit: 10 matches/hour"*) ;; *) fail "11th match not capped: $err" ;; esac
code=$(q -c "do \$\$ begin perform apply_match_result('$A','$B',1,1,1,1,1,0,0,1,'{}','{}',10);
  exception when sqlstate 'PT429' then raise warning 'PT429'; end \$\$" 2>&1)
case "$code" in *PT429*) ;; *) fail "cap did not raise sqlstate PT429: $code" ;; esac

[ "$(q -c "select wins + draws + losses from teams where id = '$A'")" = 10 ] || fail "W/D/L != 10"
[ "$(q -c "select elo from teams where id = '$A'")" = 1160 ] || fail "elo != 1160"

# Normalized email is enforced by the database, not just the API.
q -c "insert into users (email) values ('Mixed@Example.com')" 2>/dev/null && fail "unnormalized email accepted"

q -c "delete from users where id = '$A'"
left=$(q -c "select (select count(*) from profiles where id = '$A')
  + (select count(*) from teams where owner = '$A')
  + (select count(*) from matches where team_a = '$A' or team_b = '$A')")
[ "$left" = 0 ] || fail "delete did not cascade ($left rows left)"
[ "$(q -c "select count(*) from teams where id = '$B'")" = 1 ] || fail "other user's team was deleted"

echo "schema ok"
