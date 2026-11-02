// Run: node --test server/sim/
import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from './sim.mjs';
import { simulateMatch, ENGINE_VERSION } from '../../js/engine.js';
import { eloDelta, resultFromScore } from '../../js/elo.js';
import { defaultTeam } from '../../js/team.js';

const server = createServer();
await new Promise((r) => server.listen(0, '127.0.0.1', r));
const base = `http://127.0.0.1:${server.address().port}`;
test.after(() => server.close());

const post = (path, body) =>
  fetch(base + path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: typeof body === 'string' ? body : JSON.stringify(body),
  });

test('play matches the in-process engine exactly (parity)', async () => {
  const cfgA = { pressDist: 110, possessionPush: 0.16 };
  const cfgB = {};
  const res = await post('/play', {
    a: { config: cfgA, elo: 1000 },
    b: { config: cfgB, elo: 1000 },
    seed: 12345,
  });
  assert.equal(res.status, 200);
  const body = await res.json();
  const { score } = simulateMatch(cfgA, cfgB, 12345);
  assert.deepEqual(body.score, score);
  assert.equal(body.deltaA, eloDelta(1000, 1000, resultFromScore(score[0], score[1])));
  assert.equal(body.engineVersion, ENGINE_VERSION);
});

test('validate: real config ok, junk rejected, order kept', async () => {
  const res = await post('/validate', { configs: [defaultTeam('Default AI'), { formation: 'nope' }] });
  assert.equal(res.status, 200);
  const { results } = await res.json();
  assert.equal(results.length, 2);
  assert.deepEqual(results[0], { ok: true, errors: [] });
  assert.equal(results[1].ok, false);
  assert.ok(results[1].errors.length > 0);
});

test('bad input is a 400, not a crash', async () => {
  assert.equal((await post('/play', '{not json')).status, 400);
  assert.equal((await post('/play', { a: {}, b: {}, seed: 1 })).status, 400);
  assert.equal((await post('/play', { a: { config: {}, elo: 1000 }, b: { config: {}, elo: 1000 }, seed: -1 })).status, 400);
  assert.equal((await post('/validate', { configs: 'x' })).status, 400);
});

test('healthz and unknown routes', async () => {
  const h = await fetch(base + '/healthz');
  assert.equal(h.status, 200);
  assert.equal(await h.text(), 'ok');
  assert.equal((await post('/nope', {})).status, 404);
});
