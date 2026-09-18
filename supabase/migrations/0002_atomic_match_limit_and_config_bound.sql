-- 0002: enforce the per-team hourly match cap in the database, and put a size
-- fence on teams.config.
--
-- The cap used to be a count-then-act check in play-match's JS: every request
-- in a parallel burst read the same pre-increment count and every one of them
-- passed, so 10/hour was bypassable by firing requests at once. Moving the
-- check inside apply_match_result puts it in the same transaction as the
-- insert, behind a transaction-scoped advisory lock on team_a, so concurrent
-- matches for one team serialise and the ones over the line lose.
--
-- p_limit is opt-in: NULL (the default) or <= 0 means no cap. That keeps
-- scripts/seed-league.mjs working unchanged — it calls this RPC 21 times
-- back-to-back as service_role and passes only the original 12 arguments, so
-- it gets the unlimited path. Only play-match sends a number.

-- Adding a parameter changes the signature, and `create or replace` cannot do
-- that: it would create a second, 13-argument function alongside the old one
-- and every 12-argument call would then fail as ambiguous. So: drop, create,
-- and re-issue the grants the drop takes with it.
drop function public.apply_match_result(
  uuid, uuid, int, int, bigint, int, int, int, int, int, jsonb, jsonb
);

create function public.apply_match_result(
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
  -- Max matches team_a may have started in the last hour, counting this one.
  -- NULL or <= 0 = unlimited (seeding, backfills).
  p_limit int default null
) returns uuid
language plpgsql
security definer
set search_path = public
as $$
declare
  m_id uuid;
  recent int;
begin
  if p_limit is not null and p_limit > 0 then
    -- Serialise other capped calls for this team until this transaction ends.
    -- Without it the count below is just a read, and a parallel burst all
    -- reads the same number. Transaction-scoped: released on commit/rollback,
    -- nothing to unlock, nothing to leak if this function raises.
    perform pg_advisory_xact_lock(
      hashtext('apply_match_result:' || p_team_a::text)::bigint
    );

    select count(*) into recent
      from matches
     where team_a = p_team_a
       and created_at >= now() - interval '1 hour';

    if recent >= p_limit then
      -- PostgREST maps a PTnnn sqlstate onto HTTP nnn, and play-match matches
      -- on the code to stop a placement run at the cap instead of erroring.
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

-- Only the Edge Function (service role) may apply results. Same as 0001; the
-- drop above took the old grants with it.
revoke execute on function public.apply_match_result from public, anon, authenticated;
grant execute on function public.apply_match_result to service_role;

-- ---------- teams.config size fence ----------

-- teams.config is jsonb and 0001 grants authenticated INSERT/UPDATE on it, so
-- any signed-in user can PATCH arbitrary JSON straight through PostgREST
-- without going near the builder. play-match re-validates with validateTeam
-- before it simulates, so a junk blob can't corrupt a match — but the column
-- is publicly readable and fetchLeaderboard ships it to every visitor, so an
-- unbounded blob is a free storage and bandwidth sink for anyone with an
-- account.
--
-- This is a fence, not a schema. The real schema check is validateTeam in
-- js/team.js; restating it in SQL would just give it a second copy to drift
-- from. All this promises is: an object, and not enormous.
--
-- Sizing: every real config is ~480 bytes of JSON (the 7 seeded house teams
-- measure 475-483, and a user team is the same 12 keys with a different name),
-- which is 876 bytes of jsonb at the widest — measured, not guessed. 8 KB is
-- ~9x that: room for a lot of future fields, still far too small to be worth
-- abusing.
alter table public.teams
  add constraint teams_config_sane check (
    jsonb_typeof(config) = 'object' and pg_column_size(config) <= 8192
  );
