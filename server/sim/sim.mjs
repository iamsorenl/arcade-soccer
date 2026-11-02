// sim: the League's match runner. Internal only (the Go API is the only
// caller); never touches the database. It imports the game's own validator,
// engine and Elo math verbatim, so a ranked match is the exact same simulation
// the browser runs: there is no second copy of the rules to drift.
//
//   POST /validate {configs: object[]}                   -> {results: [{ok, errors}]}
//   POST /play {a: {config, elo}, b: {config, elo}, seed} -> {score, deltaA, engineVersion}
//   GET  /healthz                                        -> ok
//
// Run: PORT=8081 node server/sim/sim.mjs

import http from 'node:http';
import { pathToFileURL } from 'node:url';
import { validateTeam } from '../../js/team.js';
import { simulateMatch, ENGINE_VERSION } from '../../js/engine.js';
import { eloDelta, resultFromScore } from '../../js/elo.js';

const MAX_BODY = 256 * 1024; // placement validates a whole ladder of ~1 KB configs

class BadRequest extends Error {}

function readJSON(req) {
  return new Promise((resolve, reject) => {
    let size = 0;
    const chunks = [];
    req.on('data', (c) => {
      size += c.length;
      if (size > MAX_BODY) {
        reject(new BadRequest('body too large'));
        req.destroy();
      } else chunks.push(c);
    });
    req.on('end', () => {
      try {
        resolve(JSON.parse(Buffer.concat(chunks).toString('utf8')));
      } catch {
        reject(new BadRequest('invalid JSON body'));
      }
    });
    req.on('error', reject);
  });
}

const isObj = (v) => v !== null && typeof v === 'object' && !Array.isArray(v);

function side(v, label) {
  if (!isObj(v) || !isObj(v.config) || !Number.isInteger(v.elo)) {
    throw new BadRequest(`${label} must be {config: object, elo: integer}`);
  }
  return v;
}

function validate(body) {
  if (!isObj(body) || !Array.isArray(body.configs)) {
    throw new BadRequest('body must be {configs: object[]}');
  }
  return {
    results: body.configs.map((c) => {
      const r = validateTeam(c);
      return { ok: r.ok, errors: r.errors || [] };
    }),
  };
}

function play(body) {
  if (!isObj(body)) throw new BadRequest('body must be an object');
  const a = side(body.a, 'a');
  const b = side(body.b, 'b');
  const seed = body.seed;
  if (!Number.isInteger(seed) || seed < 0 || seed > 0xffffffff) {
    throw new BadRequest('seed must be an integer in 0..2^32-1');
  }
  const { score } = simulateMatch(a.config, b.config, seed);
  const deltaA = eloDelta(a.elo, b.elo, resultFromScore(score[0], score[1]));
  return { score, deltaA, engineVersion: ENGINE_VERSION };
}

const routes = { 'POST /validate': validate, 'POST /play': play };

export function createServer() {
  return http.createServer(async (req, res) => {
    const send = (status, body) => {
      res.writeHead(status, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify(body));
    };
    if (req.method === 'GET' && req.url === '/healthz') {
      res.writeHead(200, { 'Content-Type': 'text/plain' });
      return res.end('ok');
    }
    const handler = routes[`${req.method} ${req.url}`];
    if (!handler) return send(404, { error: 'not found' });
    try {
      send(200, handler(await readJSON(req)));
    } catch (e) {
      if (e instanceof BadRequest) return send(400, { error: e.message });
      console.error(e);
      send(500, { error: 'sim failed' });
    }
  });
}

if (import.meta.url === pathToFileURL(process.argv[1]).href) {
  const port = Number(process.env.PORT || 8081);
  createServer().listen(port, () => console.log(`sim listening on :${port}`));
}
