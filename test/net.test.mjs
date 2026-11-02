// net.js against a stubbed fetch/localStorage/location: magic-link handoff,
// session handling, and the offline -> maintenance mapping.
// Run with: node test/net.test.mjs

import assert from 'node:assert/strict';
import { LEAGUE_API_URL } from '../js/league-config.js';

const store = new Map();
globalThis.localStorage = {
  getItem: (k) => (store.has(k) ? store.get(k) : null),
  setItem: (k, v) => store.set(k, String(v)),
  removeItem: (k) => store.delete(k),
};
globalThis.location = { hash: '', pathname: '/arcade-soccer/', search: '' };
globalThis.history = { replaceState: () => { location.hash = ''; } };

let routes = {};
const calls = [];
globalThis.fetch = async (url, opts = {}) => {
  const path = url.slice(LEAGUE_API_URL.length).split('?')[0];
  const key = `${opts.method} ${path}`;
  calls.push({ key, auth: opts.headers?.Authorization, body: opts.body && JSON.parse(opts.body) });
  const r = routes[key];
  if (!r) throw new TypeError('Failed to fetch');
  if (r instanceof Error) throw r;
  return new Response(JSON.stringify(r.body), { status: r.status ?? 200 });
};

const net = await import('../js/net.js');
const reset = () => { store.clear(); calls.length = 0; routes = {}; location.hash = ''; };
const ME = { user: { id: 'u1', email: 'a@b.co' }, profile: { id: 'u1', username: 'alice' }, team: null };

// 1. #login=<token> is traded for a session, stored, and scrubbed from the URL.
reset();
location.hash = '#login=tok123';
routes['POST /auth/verify'] = { body: { session: 'sess1', user: ME.user } };
routes['GET /me'] = { body: ME };
assert.deepEqual(await net.getUser(), ME.user);
assert.equal(store.get('league.session'), 'sess1');
assert.equal(location.hash, '');
assert.deepEqual(calls[0].body, { token: 'tok123' });
assert.equal(calls[1].auth, 'Bearer sess1');
// Follow-up reads come from the cached /me, no extra round trip.
assert.deepEqual(await net.getProfile('ignored'), ME.profile);
assert.equal(await net.getMyTeam('ignored'), null);
assert.equal(calls.length, 2);

// 2. No session: signed out without calling the server.
reset();
assert.equal(await net.getUser(), null);
assert.equal(calls.length, 0);

// 3. 401 on /me (expired/revoked): signed out and the stale token is forgotten.
reset();
store.set('league.session', 'old');
routes['GET /me'] = { status: 401, body: { error: 'not signed in' } };
assert.equal(await net.getUser(), null);
assert.equal(store.has('league.session'), false);

// 4. Server down / unreachable -> LeagueOffline carrying the maintenance copy.
for (const r of [{ status: 503, body: {} }, { status: 502, body: {} }, { status: 504, body: {} }, undefined]) {
  reset();
  if (r) routes['GET /leaderboard'] = r;
  await assert.rejects(net.fetchLeaderboard(), (e) => {
    assert.ok(e instanceof net.LeagueOffline);
    assert.equal(e.message, 'The AI League is under maintenance. The rest of the game still works. Check back soon.');
    return true;
  });
}

// 5. Real server errors keep their message (and are not "maintenance").
reset();
store.set('league.session', 's');
routes['POST /play'] = { status: 429, body: { error: 'rate limit: 10 matches/hour' } };
await assert.rejects(net.challenge('t1', 't2'), (e) => {
  assert.ok(!(e instanceof net.LeagueOffline));
  assert.equal(e.message, 'rate limit: 10 matches/hour');
  assert.equal(e.status, 429);
  return true;
});

// 6. Request shapes the API expects.
reset();
store.set('league.session', 's');
routes['PUT /team'] = { body: { id: 't1', name: 'X', elo: 1000, version: 2 } };
routes['POST /play'] = { body: { mode: 'placement', matches: [], elo: 1000 } };
routes['GET /matches'] = { body: [] };
const team = { name: 'X', version: 2, formation: 'diamond' };
await net.publishTeam('u1', team);
assert.deepEqual(calls[0].body, { name: 'X', config: team, version: 2 });
await net.runPlacement('t1');
assert.deepEqual(calls[1].body, { mode: 'placement', teamId: 't1' });
await net.challenge('t1', 't2');
assert.deepEqual(calls[2].body, { mode: 'challenge', teamId: 't1', opponentId: 't2' });
await net.fetchMatches('t1', 20);

// 7. Sign out clears the session even if the server is unreachable.
reset();
store.set('league.session', 's');
await assert.rejects(net.signOut());
assert.equal(store.has('league.session'), false);

// 8. localStorage throwing (private mode) means signed out, not a crash.
reset();
const saved = globalThis.localStorage;
globalThis.localStorage = { getItem() { throw new Error('denied'); }, setItem() { throw new Error('denied'); }, removeItem() { throw new Error('denied'); } };
assert.equal(await net.getUser(), null);
globalThis.localStorage = saved;

console.log('net ok');
