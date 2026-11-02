-- AI League schema, self-hosted. Same tables as supabase/migrations/0001-0003,
-- minus the Supabase-only parts (auth.users, RLS, anon/authenticated grants,
-- keepalive). Who-may-write-what used to be RLS + column grants; it now lives
-- in the Go API, which takes the user id from the session and nothing else.
--
-- Safe to apply repeatedly: every statement is if-not-exists / or-replace.

create table if not exists users (
  id uuid primary key default gen_random_uuid(),
  -- Stored normalized so a returning user with different case/spacing lands on
  -- the same account. The API normalizes too; this makes it impossible not to.
  email text not null unique check (email = lower(btrim(email)) and email <> ''),
  created_at timestamptz not null default now()
);

create table if not exists profiles (
  id uuid primary key references users (id) on delete cascade,
  username text not null unique check (char_length(username) between 3 and 20),
  created_at timestamptz not null default now()
);

create table if not exists teams (
  id uuid primary key default gen_random_uuid(),
  owner uuid not null unique references profiles (id) on delete cascade,
  name text not null check (char_length(name) between 1 and 24),
  -- A fence, not a schema (see 0002): validateTeam in js/team.js is the real
  -- check. Real configs are ~480 bytes of JSON; 8 KB is ~9x that.
  config jsonb not null constraint teams_config_sane check (
    jsonb_typeof(config) = 'object' and pg_column_size(config) <= 8192
  ),
  version int not null default 1,
  elo int not null default 1000,
  wins int not null default 0,
  draws int not null default 0,
  losses int not null default 0,
  created_at timestamptz not null default now()
);

create table if not exists matches (
  id uuid primary key default gen_random_uuid(),
  team_a uuid not null references teams (id) on delete cascade,
  team_b uuid not null references teams (id) on delete cascade,
  version_a int not null,
  version_b int not null,
  seed bigint not null,
  score_a int not null,
  score_b int not null,
  elo_delta_a int not null,
  elo_delta_b int not null,
  engine_version int not null,
  -- Config snapshots at play time so replays survive later team edits.
  config_a jsonb not null,
  config_b jsonb not null,
  created_at timestamptz not null default now()
);

create index if not exists matches_team_a_created_idx on matches (team_a, created_at desc);
create index if not exists matches_team_b_created_idx on matches (team_b, created_at desc);
create index if not exists teams_elo_idx on teams (elo desc);

-- Magic-link tokens: 15 minutes, single use. Only the SHA-256 is stored, so a
-- leaked table can't be replayed as sign-ins.
create table if not exists login_tokens (
  token_hash bytea primary key,
  email text not null,
  expires_at timestamptz not null,
  used_at timestamptz
);

-- Sessions: 90 days, pushed forward on use. Hash-only, same reason.
create table if not exists sessions (
  token_hash bytea primary key,
  user_id uuid not null references users (id) on delete cascade,
  created_at timestamptz not null default now(),
  last_seen_at timestamptz not null default now(),
  expires_at timestamptz not null
);
create index if not exists sessions_user_idx on sessions (user_id);

-- ---------- Match application (verbatim from 0002, grants dropped) ----------

-- Inserts the match row and applies both teams' elo/W/D/L in one transaction,
-- so a failure writes nothing. p_limit caps matches team_a started in the last
-- hour (NULL or <= 0 = unlimited, for seeding/backfills); the advisory lock
-- serialises concurrent calls for one team so a parallel burst can't all read
-- the same pre-increment count. Raises sqlstate PT429 at the cap.
create or replace function apply_match_result(
  p_team_a uuid,
  p_team_b uuid,
  p_version_a int,
  p_version_b int,
  p_seed bigint,
  p_score_a int,
  p_score_b int,
  p_delta_a int,
  p_delta_b int,
  p_engine_version int,
  p_config_a jsonb,
  p_config_b jsonb,
  p_limit int default null
) returns uuid
language plpgsql
set search_path = public
as $$
declare
  m_id uuid;
  recent int;
begin
  if p_limit is not null and p_limit > 0 then
    perform pg_advisory_xact_lock(
      hashtext('apply_match_result:' || p_team_a::text)::bigint
    );

    select count(*) into recent
      from matches
     where team_a = p_team_a
       and created_at >= now() - interval '1 hour';

    if recent >= p_limit then
      raise exception 'rate limit: % matches/hour', p_limit
        using errcode = 'PT429';
    end if;
  end if;

  insert into matches (
    team_a, team_b, version_a, version_b, seed, score_a, score_b,
    elo_delta_a, elo_delta_b, engine_version, config_a, config_b
  ) values (
    p_team_a, p_team_b, p_version_a, p_version_b, p_seed, p_score_a, p_score_b,
    p_delta_a, p_delta_b, p_engine_version, p_config_a, p_config_b
  ) returning id into m_id;

  update teams set
    elo = elo + p_delta_a,
    wins = wins + (p_score_a > p_score_b)::int,
    draws = draws + (p_score_a = p_score_b)::int,
    losses = losses + (p_score_a < p_score_b)::int
  where id = p_team_a;

  update teams set
    elo = elo + p_delta_b,
    wins = wins + (p_score_b > p_score_a)::int,
    draws = draws + (p_score_b = p_score_a)::int,
    losses = losses + (p_score_b < p_score_a)::int
  where id = p_team_b;

  return m_id;
end;
$$;
