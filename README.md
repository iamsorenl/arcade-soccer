# Fable-5-Soccer-Game
How far can Fable 5 go!

## Arcade Soccer

A 4v4 top-down arcade soccer game — vanilla JS + Canvas, no dependencies, no build step.

**Play it:** https://iamsorenl.github.io/Fable-5-Soccer-Game/

Single player vs AI (Easy/Normal/Hard) or local 2-player on one keyboard.

## AI League

An AI team is a **config document** run by the shared deterministic engine: formation,
tactical weights, and per-player attribute splits under a shared point budget — no user
code. Build yours in the Team Builder, test it locally (Watch = AI vs AI, Play = you vs it),
then publish it to compete on an Elo ladder. Ranked matches are simulated **server-side**
in a Supabase Edge Function that runs the very same `js/engine.js` and `js/team.js` the
browser uses, so results are cheat-proof. A match row stores just seed + config snapshots
+ score, which makes replays free: the browser re-runs the sim from the seed with
rendering on. Matches recorded by an older engine version show a notice when replayed.

- **Team Builder** — sliders over the schema, 80-point budget meter, localStorage persistence, preset opponents (Default/Balanced/Aggressive/The Wall).
- **AI League** — magic-link sign-in, leaderboard (rank/owner/Elo/W-D-L/form), Challenge button, per-team match history, replay viewer.
- **Publish** (in the builder) — upserts your team and runs ~5 placement matches vs nearest-Elo teams.

Everything except Publish/Challenge works signed out, and the whole game works with no
backend configured at all.

## Local dev

```sh
python3 -m http.server            # any static server; open http://localhost:8000
node test/determinism.test.mjs    # same (configs, seed) => identical match — must never break
node test/team.test.mjs           # schema validation + budget + attribute plumbing
node test/elo.test.mjs            # Elo expected-score symmetry + K application
```

## Supabase deploy (one-time, manual)

1. Create a project at https://supabase.com (any name). Email auth (magic links) is on by default.
2. Install the CLI (`brew install supabase/tap/supabase`), then from the repo root:
   ```sh
   supabase login
   supabase link --project-ref <YOUR-PROJECT-REF>
   supabase db push                      # applies supabase/migrations/0001_init.sql
   supabase functions deploy play-match  # bundles the fn + the shared js/ modules
   ```
   The function imports `js/engine.js` / `js/team.js` / `js/elo.js` by relative path;
   if your CLI version refuses to bundle files outside `supabase/functions/`, copy the
   modules the import graph pulls in (`engine`, `team`, `elo`, `ai`, `actions`,
   `entities`, `physics`, `config`) into `supabase/functions/play-match/` and fix the
   import paths.
3. In Supabase **Settings → API**, copy the project URL and `anon` public key into
   `js/supabase-config.js` (the anon key is public by design — RLS is the security
   boundary), commit, and push. GitHub Pages serves the rest.
4. In **Authentication → URL Configuration**, add your Pages URL
   (`https://<user>.github.io/Fable-5-Soccer-Game/`) as a redirect URL so magic links
   land back in the game.

## Seeding the ladder

An empty `teams` table isn't just a blank leaderboard: placement rates a new team
against the *nearest-Elo teams in the table*, so with nothing in it, publishing places
you against no one. `scripts/seed-league.mjs` fills it with seven house teams — the four
builder presets plus Tiki-Taka, Counter Attack and Sunday League — then plays a
round-robin so the ladder opens with real ratings, W-D-L and replayable history.

```sh
SUPABASE_SERVICE_ROLE_KEY=... node scripts/seed-league.mjs           # seed
SUPABASE_SERVICE_ROLE_KEY=... node scripts/seed-league.mjs --reset   # wipe and reseed
```

The key is in **Settings → API → service_role**. It bypasses RLS, so pass it on the
command line only — never commit it, never put it in CI. Seeds are fixed, so a reseed
reproduces the identical ladder.

Each house team needs a bot auth user, because `teams.owner` is a NOT NULL unique FK to
`profiles.id`, itself a FK to `auth.users.id`. They own usernames prefixed `house-` so
the ladder doesn't read as having players it doesn't have. `--reset` deletes those users
and the FK cascade clears their profiles, teams and matches.

Match results go through the same `apply_match_result` RPC that `play-match` uses, so
there's no second copy of the Elo or match-writing logic. `test/team.test.mjs` validates
every house config against the schema and point budget, so a malformed archetype fails
locally rather than against live data.

## Keeping the project awake

Free-tier Supabase projects pause after ~7 days with no requests, which takes the whole
AI League offline — DNS stops resolving — until you restore it by hand from the
dashboard. `.github/workflows/keep-supabase-awake.yml` pings the REST API every other
day to reset that clock. It needs no secret (it reads the public anon key straight out
of `js/supabase-config.js`) and **fails the run on a non-200**, so a paused project
emails you instead of failing quietly.

Two things it can't do for you: GitHub disables scheduled workflows after 60 days with
no repo activity — re-enable from the Actions tab — and scheduled workflows only run
from the default branch, so the file has to be on `main` to fire at all.

### Credits

Player sprites from the [Kenney Sports Pack](https://opengameart.org/content/sports-pack-350) (CC0).
