// play-match Edge Function: the only writer of ranked results. Authenticates
// the caller, validates configs with the exact client validator, simulates
// with the exact client engine (same files, imported verbatim), and applies
// Elo + the match row in one transaction via apply_match_result.
//
// Body: { mode: 'challenge', teamId, opponentId }  -> one match
//       { mode: 'placement', teamId }              -> ~5 matches vs nearest elo

import { createClient } from "npm:@supabase/supabase-js@2";
// The game's own modules — plain ES modules, Deno-safe, no browser globals.
import { validateTeam } from "../../../js/team.js";
import { simulateMatch, ENGINE_VERSION } from "../../../js/engine.js";
import { eloDelta, resultFromScore } from "../../../js/elo.js";

const CHALLENGES_PER_HOUR = 10; // per-team hourly rate limit (both modes)
const PLACEMENT_COUNT = 5;
// sqlstate apply_match_result raises when the cap is reached (0002). The limit
// is enforced in the database, inside the same transaction as the insert —
// checking it here first would only be a second copy to drift from, and a
// count-then-act check in JS is exactly what parallel requests walk through.
const RATE_LIMITED = "PT429";

const CORS = {
  "Access-Control-Allow-Origin": "*",
  "Access-Control-Allow-Headers":
    "authorization, x-client-info, apikey, content-type",
};

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { ...CORS, "Content-Type": "application/json" },
  });
}

type TeamRow = {
  id: string;
  name: string;
  owner: string;
  config: Record<string, unknown>;
  version: number;
  elo: number;
};

// Simulate one match with a server-generated seed and persist it atomically.
// Mutates a.elo / b.elo so a placement run rates each match off live ratings.
// Returns null when a's hourly cap is already full — the caller decides
// whether that's a 429 or just the end of a placement run.
async function playOne(service: ReturnType<typeof createClient>, a: TeamRow, b: TeamRow) {
  const seed = crypto.getRandomValues(new Uint32Array(1))[0];
  const { score } = simulateMatch(a.config, b.config, seed);
  const deltaA = eloDelta(a.elo, b.elo, resultFromScore(score[0], score[1]));
  const deltaB = -deltaA;

  const { error } = await service.rpc("apply_match_result", {
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
    p_limit: CHALLENGES_PER_HOUR,
  });
  if (error) {
    if (error.code === RATE_LIMITED) return null;
    throw new Error(`apply_match_result: ${error.message}`);
  }

  a.elo += deltaA;
  b.elo += deltaB;
  return { opponent: b.name, opponentId: b.id, seed, score, deltaA };
}

Deno.serve(async (req) => {
  if (req.method === "OPTIONS") return new Response("ok", { headers: CORS });
  if (req.method !== "POST") return json({ error: "POST only" }, 405);

  const url = Deno.env.get("SUPABASE_URL")!;
  // Caller-scoped client for auth; service client for reads/writes (bypasses
  // RLS — this function is the trusted match runner).
  const auth = createClient(url, Deno.env.get("SUPABASE_ANON_KEY")!, {
    global: { headers: { Authorization: req.headers.get("Authorization") ?? "" } },
  });
  const service = createClient(url, Deno.env.get("SUPABASE_SERVICE_ROLE_KEY")!);

  const { data: { user } } = await auth.auth.getUser();
  if (!user) return json({ error: "not signed in" }, 401);

  let body: { mode?: string; teamId?: string; opponentId?: string };
  try {
    body = await req.json();
  } catch {
    return json({ error: "invalid JSON body" }, 400);
  }
  const { mode, teamId, opponentId } = body;
  if (!teamId || (mode !== "challenge" && mode !== "placement")) {
    return json({ error: "mode must be challenge or placement, with teamId" }, 400);
  }

  // The caller's team: must exist, be theirs, and validate against the schema
  // and point budget (re-checked server-side; the builder is not trusted).
  const { data: mine } = await service
    .from("teams")
    .select("id, name, owner, config, version, elo")
    .eq("id", teamId)
    .single();
  if (!mine) return json({ error: "team not found" }, 404);
  if (mine.owner !== user.id) return json({ error: "not your team" }, 403);
  const check = validateTeam(mine.config);
  if (!check.ok) return json({ error: "invalid team config", details: check.errors }, 422);

  const capped = () =>
    json({ error: `rate limit: ${CHALLENGES_PER_HOUR} matches/hour` }, 429);

  if (mode === "challenge") {
    if (!opponentId || opponentId === teamId) {
      return json({ error: "challenge needs a different opponentId" }, 400);
    }
    const { data: opp } = await service
      .from("teams")
      .select("id, name, owner, config, version, elo")
      .eq("id", opponentId)
      .single();
    if (!opp) return json({ error: "opponent not found" }, 404);
    if (!validateTeam(opp.config).ok) {
      return json({ error: "opponent config is invalid (stale schema?)" }, 422);
    }

    const result = await playOne(service, mine as TeamRow, opp as TeamRow);
    if (!result) return capped();
    return json({ mode, matches: [result], elo: mine.elo });
  }

  // Placement: ~5 matches vs the nearest-elo teams. Small ladder, so fetch
  // all candidates and sort here; revisit if the table grows past thousands.
  const { data: others } = await service
    .from("teams")
    .select("id, name, owner, config, version, elo")
    .neq("id", teamId);
  const candidates = (others ?? [])
    .filter((t) => validateTeam(t.config).ok)
    .sort((x, y) => Math.abs(x.elo - mine.elo) - Math.abs(y.elo - mine.elo))
    .slice(0, PLACEMENT_COUNT);
  if (candidates.length === 0) {
    return json({ mode, matches: [], elo: mine.elo, note: "no opponents yet" });
  }

  // The cap is checked per match, not once up front: a call made at 9/10 used
  // to run all 5 and land at 14. Now the 10th lands and the rest stop, and a
  // short run is a normal 200 with however many actually happened.
  const results = [];
  for (const opp of candidates) {
    const result = await playOne(service, mine as TeamRow, opp as TeamRow);
    if (!result) break;
    results.push(result);
  }
  if (results.length === 0) return capped();
  const note = results.length < candidates.length
    ? `stopped at the ${CHALLENGES_PER_HOUR}/hour cap`
    : undefined;
  return json({ mode, matches: results, elo: mine.elo, note });
});
