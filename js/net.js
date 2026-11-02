// League data layer: talks to the self-hosted League API (server/api). Nothing
// here runs unless a league feature is opened and league-config.js is filled
// in, so the game itself works fully offline / signed out.
//
// Sessions: a bearer token in localStorage, not a cookie. The page (github.io)
// and the API are different sites, and browsers block cross-site cookies.

import { LEAGUE_API_URL } from './league-config.js';

const SESSION_KEY = 'league.session';
const TIMEOUT_MS = 8000;

// The server is unreachable or down for maintenance (network error, timeout,
// 502/503/504). The message is the player-facing copy, so every place that
// shows e.message (League screen, builder publish) says the same thing.
export const MAINTENANCE =
  'The AI League is under maintenance. The rest of the game still works. Check back soon.';
export class LeagueOffline extends Error {
  constructor() {
    super(MAINTENANCE);
    this.name = 'LeagueOffline';
  }
}

export function isConfigured() {
  return !LEAGUE_API_URL.startsWith('TODO');
}

// localStorage can throw (private mode, blocked site data): treat as signed out.
function loadSession() {
  try { return localStorage.getItem(SESSION_KEY); } catch { return null; }
}
function saveSession(token) {
  try {
    if (token) localStorage.setItem(SESSION_KEY, token);
    else localStorage.removeItem(SESSION_KEY);
  } catch { /* signed in for this page load only */ }
}

async function api(method, path, body) {
  const headers = {};
  const token = loadSession();
  if (token) headers.Authorization = `Bearer ${token}`;
  if (body !== undefined) headers['Content-Type'] = 'application/json';

  let res;
  try {
    res = await fetch(LEAGUE_API_URL + path, {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
      signal: AbortSignal.timeout(TIMEOUT_MS),
    });
  } catch {
    throw new LeagueOffline();
  }
  if (res.status === 502 || res.status === 503 || res.status === 504) {
    throw new LeagueOffline();
  }
  let data = null;
  try { data = await res.json(); } catch { /* empty or non-JSON body */ }
  if (!res.ok) {
    const err = new Error((data && data.error) || `league server error (${res.status})`);
    err.status = res.status;
    throw err;
  }
  return data;
}

// ---------- Auth ----------

// The current /me ({user, profile, team}), refreshed by getUser() and kept in
// step by createProfile/publishTeam so the follow-up reads need no round trip.
let me = null;

// Magic link lands here as #login=<token> (a fragment, so mail scanners that
// prefetch links never send the token to the server). Trade it for a session
// and scrub it from the address bar.
async function consumeLoginLink() {
  const m = /(?:^#|&)login=([^&]+)/.exec(location.hash);
  if (!m) return;
  history.replaceState(null, '', location.pathname + location.search);
  const { session } = await api('POST', '/auth/verify', { token: decodeURIComponent(m[1]) });
  saveSession(session);
}

export async function getUser() {
  await consumeLoginLink();
  if (!loadSession()) {
    me = null;
    return null;
  }
  try {
    me = await api('GET', '/me');
  } catch (e) {
    if (e.status !== 401) throw e;
    saveSession(null); // expired or revoked: forget it
    me = null;
    return null;
  }
  return me.user;
}

export async function signIn(email) {
  await api('POST', '/auth/start', { email });
}

export async function signOut() {
  try {
    await api('POST', '/auth/signout');
  } finally {
    saveSession(null);
    me = null;
  }
}

async function current() {
  if (!me) await getUser();
  return me;
}

// ---------- Profiles / teams ----------
// userId params are kept for the existing call sites; the server always acts
// on the signed-in user from the session, never on an id the client sends.

export async function getProfile(_userId) {
  return (await current())?.profile ?? null;
}

export async function createProfile(_userId, username) {
  const profile = await api('POST', '/profile', { username });
  if (me) me.profile = profile;
  return profile;
}

// One published team per user: the server upserts on the caller.
export async function publishTeam(_userId, team) {
  const row = await api('PUT', '/team', { name: team.name, config: team, version: team.version });
  if (me) me.team = row;
  return row;
}

export async function getMyTeam(_userId) {
  return (await current())?.team ?? null;
}

// ---------- Leaderboard / matches ----------

export async function fetchLeaderboard(limit = 50) {
  return api('GET', `/leaderboard?limit=${limit}`);
}

// Recent matches, newest first; optionally only those involving one team.
export async function fetchMatches(teamId = null, limit = 100) {
  const q = new URLSearchParams({ limit: String(limit) });
  if (teamId) q.set('team', teamId);
  return api('GET', `/matches?${q}`);
}

// ---------- Ranked play ----------

export function challenge(teamId, opponentId) {
  return api('POST', '/play', { mode: 'challenge', teamId, opponentId });
}

export function runPlacement(teamId) {
  return api('POST', '/play', { mode: 'placement', teamId });
}
