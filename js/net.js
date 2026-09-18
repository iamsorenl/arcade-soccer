// Supabase data layer. supabase-js is loaded from the CDN lazily on first
// use, so the game itself works fully offline / signed out — nothing here
// runs unless a league feature is opened and supabase-config.js is filled in.

import { SUPABASE_URL, SUPABASE_ANON_KEY } from './supabase-config.js';

let client = null;

export function isConfigured() {
  return !SUPABASE_URL.startsWith('TODO') && !SUPABASE_ANON_KEY.startsWith('TODO');
}

async function getClient() {
  if (!client) {
    // pinned to an exact version, not a floating "@2" major-range: esm.sh
    // resolves floating ranges at request time, so the served bytes could
    // change with no change to this repo. bumping this is a deliberate act.
    const { createClient } = await import(
      'https://esm.sh/@supabase/supabase-js@2.116.0'
    );
    client = createClient(SUPABASE_URL, SUPABASE_ANON_KEY);
  }
  return client;
}

// Throws on error so callers surface one message; returns data otherwise.
function unwrap({ data, error }) {
  if (error) throw new Error(error.message);
  return data;
}

// ---------- Auth ----------

export async function getUser() {
  const supa = await getClient();
  const { data } = await supa.auth.getUser();
  return data.user || null;
}

// Magic-link sign-in: Supabase emails a link that redirects back here and
// supabase-js picks the session out of the URL automatically.
export async function signIn(email) {
  const supa = await getClient();
  unwrap(await supa.auth.signInWithOtp({
    email,
    options: { emailRedirectTo: location.href },
  }));
}

export async function signOut() {
  const supa = await getClient();
  await supa.auth.signOut();
}

// ---------- Profiles / teams ----------

export async function getProfile(userId) {
  const supa = await getClient();
  return unwrap(
    await supa.from('profiles').select('id, username').eq('id', userId).maybeSingle()
  );
}

export async function createProfile(userId, username) {
  const supa = await getClient();
  return unwrap(
    await supa.from('profiles').insert({ id: userId, username }).select().single()
  );
}

// One published team per user (teams.owner is unique): upsert on owner.
export async function publishTeam(userId, team) {
  const supa = await getClient();
  return unwrap(
    await supa
      .from('teams')
      .upsert(
        { owner: userId, name: team.name, config: team, version: team.version },
        { onConflict: 'owner' }
      )
      .select('id, name, elo, version')
      .single()
  );
}

export async function getMyTeam(userId) {
  const supa = await getClient();
  return unwrap(
    await supa
      .from('teams')
      .select('id, name, elo, version')
      .eq('owner', userId)
      .maybeSingle()
  );
}

// ---------- Leaderboard / matches ----------

export async function fetchLeaderboard(limit = 50) {
  const supa = await getClient();
  return unwrap(
    await supa
      .from('teams')
      // owner (auth.users UUID) deliberately excluded: this row is public
      // and nothing in the UI reads it (league.js keys off t.id, not owner).
      .select('id, name, elo, wins, draws, losses, config, profiles(username)')
      .order('elo', { ascending: false })
      .limit(limit)
  );
}

// Recent matches, newest first; optionally only those involving one team.
// Also used to compute leaderboard "recent form" client-side in one query.
export async function fetchMatches(teamId = null, limit = 100) {
  const supa = await getClient();
  let q = supa
    .from('matches')
    .select(
      'id, team_a, team_b, score_a, score_b, elo_delta_a, elo_delta_b, seed, engine_version, config_a, config_b, created_at'
    )
    .order('created_at', { ascending: false })
    .limit(limit);
  if (teamId) q = q.or(`team_a.eq.${teamId},team_b.eq.${teamId}`);
  return unwrap(await q);
}

// ---------- Ranked play (play-match Edge Function) ----------

async function invokePlayMatch(body) {
  const supa = await getClient();
  const { data, error } = await supa.functions.invoke('play-match', { body });
  // FunctionsHttpError hides the response body; dig the real message out.
  if (error) {
    let msg = error.message;
    try {
      const detail = await error.context.json();
      if (detail && detail.error) msg = detail.error;
    } catch { /* keep the generic message */ }
    throw new Error(msg);
  }
  return data;
}

export function challenge(teamId, opponentId) {
  return invokePlayMatch({ mode: 'challenge', teamId, opponentId });
}

export function runPlacement(teamId) {
  return invokePlayMatch({ mode: 'placement', teamId });
}
