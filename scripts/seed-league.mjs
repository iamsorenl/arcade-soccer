// Seeds the AI League with house teams so the ladder isn't a blank list.
//
// teams.owner is a NOT NULL unique FK to profiles.id, which is itself a FK to
// auth.users.id — a team cannot exist without a real auth user. So each house
// team gets a bot user, created through the admin API with the service-role
// key. They're named house-* on purpose: a ladder that looks like it has real
// players it doesn't have is worse than an empty one.
//
// Then a round-robin: every pair plays once through the same js/engine.js the
// browser and the Edge Function use, rated by js/elo.js, and recorded with the
// apply_match_result RPC — the same call play-match makes. No second copy of
// the Elo or match-writing logic lives here.
//
// Run once, locally:
//   SUPABASE_SERVICE_ROLE_KEY=... node scripts/seed-league.mjs
//   SUPABASE_SERVICE_ROLE_KEY=... node scripts/seed-league.mjs --reset
//
// The service-role key bypasses RLS entirely. Keep it in your shell, never in
// the repo and never in CI — the keepalive workflow uses the anon key.

import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

import { simulateMatch, ENGINE_VERSION } from '../js/engine.js';
import { eloDelta, resultFromScore } from '../js/elo.js';
import { FORMATIONS, defaultTeam, validateTeam } from '../js/team.js';

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..');
const EMAIL_DOMAIN = 'arcade-soccer-house.invalid'; // RFC 2606 reserved: never deliverable

function house(slug, name, overrides) {
  return { slug, config: Object.assign(defaultTeam(name), overrides) };
}

// The four builder presets plus three archetypes chosen to give the ladder a
// top, a middle and a floor. Every players array spends the full 80 points.
export const HOUSE_TEAMS = [
  house('house-default', 'Default AI', {}),

  house('house-balanced', 'Balanced', {
    shotRange: 320,
    pressDist: 95,
    possessionPush: 0.13,
    stealCooldownS: 1.6,
  }),

  house('house-aggressive', 'Aggressive', {
    shotRange: 400,
    pressDist: 130,
    possessionPush: 0.18,
    throughPassGain: 80,
    stealCooldownS: 0.7,
    keeperClearDelayS: 0.2,
    slots: FORMATIONS.Attacking.map((s) => ({ ...s })),
    players: [
      { pace: 5, stamina: 5, power: 6, control: 4 },
      { pace: 8, stamina: 4, power: 7, control: 1 },
      { pace: 8, stamina: 4, power: 7, control: 1 },
      { pace: 8, stamina: 4, power: 7, control: 1 },
    ],
  }),

  house('house-wall', 'The Wall', {
    shotRange: 260,
    pressDist: 55,
    possessionPush: 0.04,
    throughPassGain: 180,
    stealCooldownS: 2.0,
    keeperProtectHoldS: 2.5,
    slots: FORMATIONS.Defensive.map((s) => ({ ...s })),
    players: [
      { pace: 4, stamina: 5, power: 6, control: 5 },
      { pace: 3, stamina: 7, power: 4, control: 6 },
      { pace: 3, stamina: 7, power: 4, control: 6 },
      { pace: 3, stamina: 7, power: 4, control: 6 },
    ],
  }),

  // Keeps the ball, works it forward short, shoots from the edge of the area.
  // Shot range is the single biggest lever in this engine — drop it much below
  // 300 and the side simply never shoots. Tuned against the round-robin.
  house('house-tiki', 'Tiki-Taka', {
    shotRange: 300,
    pressDist: 85,
    possessionPush: 0.20,
    throughPassGain: 60,
    stealCooldownS: 1.3,
    keeperClearDelayS: 0.8,
    keeperProtectHoldS: 1.8,
    slots: FORMATIONS.Standard.map((s) => ({ ...s })),
    players: [
      { pace: 4, stamina: 5, power: 5, control: 6 },
      { pace: 4, stamina: 6, power: 3, control: 7 },
      { pace: 4, stamina: 5, power: 3, control: 8 },
      { pace: 5, stamina: 4, power: 4, control: 7 },
    ],
  }),

  // Sits deep, wins it back, then launches one very fast striker.
  house('house-counter', 'Counter Attack', {
    shotRange: 380,
    pressDist: 60,
    possessionPush: 0.06,
    throughPassGain: 220,
    stealCooldownS: 1.0,
    keeperClearDelayS: 0.15,
    keeperProtectHoldS: 1.0,
    slots: FORMATIONS.Wide.map((s) => ({ ...s })),
    players: [
      { pace: 3, stamina: 5, power: 6, control: 6 },
      { pace: 5, stamina: 6, power: 5, control: 4 },
      { pace: 6, stamina: 5, power: 5, control: 4 },
      { pace: 10, stamina: 3, power: 5, control: 2 },
    ],
  }),

  // Meant as the floor: neutral attributes, doesn't press, doesn't push up,
  // slow to win the ball back, dawdles in its own box. It lands near the
  // bottom on goal difference but not reliably last — this engine doesn't
  // punish what football intuition says it should (an earlier version that
  // chased everything and shot from range finished 2nd, and pure passivity
  // finished 3rd). Re-tune against scripts/ dry runs, not intuition.
  house('house-sunday', 'Sunday League', {
    shotRange: 300,
    pressDist: 40,
    possessionPush: 0.02,
    throughPassGain: 40,
    stealCooldownS: 3.5,
    keeperClearDelayS: 2.0,
    keeperProtectHoldS: 0.2,
    players: [
      { pace: 5, stamina: 5, power: 5, control: 5 },
      { pace: 5, stamina: 5, power: 5, control: 5 },
      { pace: 5, stamina: 5, power: 5, control: 5 },
      { pace: 5, stamina: 5, power: 5, control: 5 },
    ],
  }),
];

const emailFor = (slug) => `${slug}@${EMAIL_DOMAIN}`;

// ---------- Supabase REST helpers (no SDK: the project has no deps) ----------

function readAnonConfig() {
  const src = readFileSync(join(ROOT, 'js/supabase-config.js'), 'utf8');
  const url = src.match(/https:\/\/[a-z0-9]+\.supabase\.co/);
  if (!url) throw new Error('no Supabase URL in js/supabase-config.js');
  return url[0];
}

function makeApi(url, key) {
  const headers = {
    apikey: key,
    Authorization: `Bearer ${key}`,
    'Content-Type': 'application/json',
  };
  return async function api(path, { method = 'GET', body, prefer } = {}) {
    const res = await fetch(`${url}${path}`, {
      method,
      headers: prefer ? { ...headers, Prefer: prefer } : headers,
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    const text = await res.text();
    if (!res.ok) throw new Error(`${method} ${path} -> ${res.status} ${text}`);
    return text ? JSON.parse(text) : null;
  };
}

// ---------- Steps ----------

async function findHouseUsers(api) {
  // The admin list is paged; this ladder is tiny, so one page is plenty.
  const { users } = await api('/auth/v1/admin/users?per_page=200');
  const slugs = new Set(HOUSE_TEAMS.map((t) => emailFor(t.slug)));
  return users.filter((u) => slugs.has(u.email));
}

async function reset(api) {
  const existing = await findHouseUsers(api);
  for (const u of existing) {
    // Cascades: auth.users -> profiles -> teams -> matches.
    await api(`/auth/v1/admin/users/${u.id}`, { method: 'DELETE' });
    console.log(`  deleted ${u.email}`);
  }
  console.log(`reset: removed ${existing.length} house user(s)`);
}

async function createTeams(api) {
  const rows = [];
  for (const { slug, config } of HOUSE_TEAMS) {
    const user = await api('/auth/v1/admin/users', {
      method: 'POST',
      body: {
        email: emailFor(slug),
        // Random and immediately discarded: nobody ever signs in as a bot.
        password: `${crypto.randomUUID()}${crypto.randomUUID()}`,
        email_confirm: true,
      },
    });
    await api('/rest/v1/profiles', {
      method: 'POST',
      body: { id: user.id, username: slug },
    });
    const [team] = await api('/rest/v1/teams', {
      method: 'POST',
      prefer: 'return=representation',
      body: {
        owner: user.id,
        name: config.name,
        config,
        version: config.version,
      },
    });
    rows.push({ id: team.id, name: team.name, config, version: team.version, elo: team.elo });
    console.log(`  created ${config.name} (${slug})`);
  }
  return rows;
}

// One match, persisted exactly the way play-match persists one. Mutates elo so
// later pairings in the round-robin rate off live ratings.
async function playOne(api, a, b, seed) {
  const { score } = simulateMatch(a.config, b.config, seed);
  const deltaA = eloDelta(a.elo, b.elo, resultFromScore(score[0], score[1]));
  const deltaB = -deltaA;

  await api('/rest/v1/rpc/apply_match_result', {
    method: 'POST',
    body: {
      p_team_a: a.id,
      p_team_b: b.id,
      p_version_a: a.version,
      p_version_b: b.version,
      p_seed: seed,
      p_score_a: score[0],
      p_score_b: score[1],
      p_delta_a: deltaA,
      p_delta_b: deltaB,
      p_engine_version: ENGINE_VERSION,
      p_config_a: a.config,
      p_config_b: b.config,
    },
  });

  a.elo += deltaA;
  b.elo += deltaB;
  return score;
}

async function roundRobin(api, teams) {
  let played = 0;
  for (let i = 0; i < teams.length; i++) {
    for (let j = i + 1; j < teams.length; j++) {
      // Fixed, position-derived seed: a --reset reseed reproduces this ladder.
      const seed = 700_000 + i * 1_000 + j;
      const score = await playOne(api, teams[i], teams[j], seed);
      console.log(
        `  ${teams[i].name} ${score[0]}-${score[1]} ${teams[j].name}`
      );
      played++;
    }
  }
  return played;
}

async function main() {
  const key = process.env.SUPABASE_SERVICE_ROLE_KEY;
  if (!key) {
    console.error(
      'SUPABASE_SERVICE_ROLE_KEY is not set.\n' +
      'Find it in Supabase > Settings > API > service_role. It bypasses RLS —\n' +
      'pass it on the command line only, never commit it.'
    );
    process.exit(1);
  }

  // Validate before touching the network: a bad archetype must never reach the
  // database, and the Edge Function would reject it as an opponent anyway.
  for (const { slug, config } of HOUSE_TEAMS) {
    const res = validateTeam(config);
    if (!res.ok) throw new Error(`${slug} is invalid: ${res.errors.join('; ')}`);
  }

  const url = readAnonConfig();
  const api = makeApi(url, key);
  const wantsReset = process.argv.includes('--reset');

  console.log(`seeding ${url}`);
  if (wantsReset) await reset(api);

  const existing = await findHouseUsers(api);
  if (existing.length > 0) {
    console.error(
      `${existing.length} house user(s) already exist. Re-run with --reset to ` +
      'wipe and reseed them.'
    );
    process.exit(1);
  }

  const teams = await createTeams(api);
  const played = await roundRobin(api, teams);

  const table = [...teams].sort((x, y) => y.elo - x.elo);
  console.log(`\nok — ${teams.length} teams, ${played} matches`);
  for (const [i, t] of table.entries()) {
    console.log(`  ${i + 1}. ${t.name.padEnd(16)} ${t.elo}`);
  }
}

// Importable for tests without running the seed.
if (process.argv[1] === fileURLToPath(import.meta.url)) {
  main().catch((e) => {
    console.error(e.message);
    process.exit(1);
  });
}
