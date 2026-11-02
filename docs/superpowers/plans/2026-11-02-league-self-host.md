# AI League Self-Host Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
>
> **Ownership matters here.** Tasks marked **Soren writes** are learning tasks: the
> agent explains the concept, shows the failing test, lets Soren write the code, then
> reviews it. The agent does not write that code unless Soren asks it to. Tasks marked
> **Claude builds** are plumbing the agent writes directly. Tasks that touch
> mce-pc-server or the tailnet policy need Soren's go-ahead at the start of the task.

**Goal:** Replace the paused-prone Supabase backend of the AI League with a Go API, a
Node match-sim service and Postgres, running in k3s on mce-pc-server behind Tailscale
Funnel. Existing accounts, teams, Elo and match history carry over.

**Architecture:** GitHub Pages serves the game unchanged. `js/net.js` talks to a Go API
(`server/api`), the only public entry. The API owns auth, sessions and every write. It
calls an internal Node service (`server/sim`) that imports `js/team.js`, `js/engine.js`
and `js/elo.js` verbatim. Postgres keeps `apply_match_result` as the single atomic
match writer.

**Tech Stack:** Go 1.27 (stdlib `net/http`, `github.com/jackc/pgx/v5`), Node 24
(stdlib `node:http`), Postgres 17, k3s, Tailscale Funnel, GitHub Actions + GHCR.

**Spec:** `docs/superpowers/specs/2026-11-02-league-self-host-design.md`

## Global Constraints

- Hourly match cap: 10 per team (`CHALLENGES_PER_HOUR`). Placement: 5 matches vs nearest Elo.
- Rate-limit sqlstate raised by `apply_match_result`: `PT429`.
- `teams.config` fence: `jsonb_typeof(config) = 'object' and pg_column_size(config) <= 8192`.
- Login tokens: 15 minutes, single use. Sessions: 90 days, extended on use. Only SHA-256 hashes are stored.
- The session token lives in `localStorage` key `league.session` and is sent as `Authorization: Bearer <token>`. No cookies.
- Magic link format: `${APP_URL}#login=<token>` (a fragment, so mail scanners never send it to the server).
- Emails are stored `lower(trim(email))`.
- CORS allows exactly the origins in `ALLOWED_ORIGINS` (prod: `https://iamsorenl.github.io`).
- Error bodies are `{"error": "<message>"}` (plus `details` on 422), with the same status codes play-match and delete-account use today.
- Maintenance copy, exact: `The AI League is under maintenance. The rest of the game still works. Check back soon.`
- Offline detection: network error, an 8000 ms timeout, or HTTP 502/503/504.
- Delete requires body `{"confirm": "DELETE"}`. The user ID always comes from the session, never from a body.
- Nothing secret is committed. DB and SMTP passwords live only in k8s Secrets and local `.env` files (gitignored).
- The game must still work fully with the League unreachable. Existing `node test/*.test.mjs` keep passing.

## Review Focus

1. **`sim` down while the API is up.** `/play` should return 503, and the player sees the maintenance message, not a raw error. Test: Task 6 `TestPlaySimDown`.
2. **A login link opened twice, or the old one after a newer one was sent.** The second use fails with "link expired or already used, request a new one", and an existing session in that browser stays valid. Test: Task 3 `TestVerifyTokenSingleUse`.
3. **A returning user types their email with different case or spaces.** They land on their migrated account, not a new one. Tests: Task 3 `TestStartNormalizesEmail`, Task 9 duplicate check.
4. **A username that's already taken.** Return 409 `username taken`, not a 500, so the publish flow can show it. Test: Task 4 `TestCreateProfileTaken`.
5. **The server drops mid-publish, after the profile is created but before the team upsert.** A retry finishes cleanly (the profile already exists and the upsert is idempotent). Test: Task 4 `TestPublishRetryAfterPartial`.

---

### Task 0: Insurance dump of Supabase (Claude builds, Soren supplies the password)

**Files:**
- Create: `scripts/dump-supabase.sh`
- Modify: `.gitignore` (add `backups/`, `.env`, `server/**/.env`)

- [ ] **Step 1:** Soren copies the DB connection string from Supabase dashboard > Connect (session pooler, port 5432) into the `SUPABASE_DB_URL` env var for this shell only.
- [ ] **Step 2:** Write `scripts/dump-supabase.sh OUT_DIR`. It runs `docker run --rm postgres:17 pg_dump` (the local pg_dump is 14, and the server is 17.6) twice:
  - a full `-Fc` dump to `OUT_DIR/supabase-full-<date>.dump`
  - `\copy (select id, email, created_at from auth.users) to OUT_DIR/auth-users-<date>.csv csv header`
- [ ] **Step 3:** Run it with `OUT_DIR=~/Backups/arcade-soccer` (outside the repo).
  - Expected: both files exist, non-empty.
  - `pg_restore --list` on the dump shows `public.teams`, `public.matches` and `public.profiles`.
- [ ] **Step 4: Commit** the script and `.gitignore` only: `git commit -m "Add a Supabase dump script for the self-host move"`.

### Task 1: New database schema (Claude builds, Soren reviews)

**Files:**
- Create: `server/db/schema.sql`
- Test: `server/db/schema_test.sh`

**Interfaces:**
- Produces these tables and the function every later task uses:
  - `users(id uuid pk default gen_random_uuid(), email text not null unique check (email = lower(trim(email))), created_at timestamptz default now())`
  - `profiles`, `teams` and `matches`, exactly as in `supabase/migrations/0001_init.sql`, except `profiles.id references users(id) on delete cascade`. Plus the `teams_config_sane` check (0002), the indexes, and the 0003 grant (dropped: there are no roles now).
  - `login_tokens(token_hash bytea pk, email text not null, expires_at timestamptz not null, used_at timestamptz)`
  - `sessions(token_hash bytea pk, user_id uuid not null references users(id) on delete cascade, created_at timestamptz default now(), last_seen_at timestamptz default now(), expires_at timestamptz not null)`
  - `apply_match_result(...)`: the 13-argument version from 0002, verbatim, minus the `grant`/`revoke` lines.
  - Drops all RLS policies, role grants and `keepalive`.
  - Wrapped in `create table if not exists` / `create or replace` so applying it twice is a no-op.

- [ ] **Step 1: Write `schema_test.sh`.** Against `TEST_DATABASE_URL` it:
  - drops and recreates schema `public`
  - applies `schema.sql` twice (it must be idempotent)
  - inserts a user, a profile and two teams
  - calls `apply_match_result(..., p_limit => 10)` 11 times for one team

  It asserts:
  - the 11th call fails with sqlstate `PT429`
  - `teams.wins + draws + losses` = 10 for that team
  - deleting the user leaves 0 rows in `profiles`, `teams` and `matches`
- [ ] **Step 2:** Run `TEST_DATABASE_URL=postgres://localhost/league_test bash server/db/schema_test.sh`. Expected: FAIL (no schema.sql yet).
- [ ] **Step 3:** Write `schema.sql`.
- [ ] **Step 4:** Run the test again. Expected: `schema ok`.
- [ ] **Step 5: Commit.** `git commit -m "Add the self-hosted League schema"`

### Task 2: `sim` service (Claude builds)

**Files:**
- Create: `server/sim/sim.mjs`, `server/sim/sim.test.mjs`

**Interfaces:**
- Produces an HTTP API on `PORT` (default 8081), internal only:
  - `POST /validate {configs: object[]}` returns `{results: [{ok: bool, errors: string[]}]}`, one result per config, in order, using `validateTeam`.
  - `POST /play {a: {config, elo}, b: {config, elo}, seed: uint32}` returns `{score: [int, int], deltaA: int, engineVersion: int}`. It uses `simulateMatch(a.config, b.config, seed).score`, `eloDelta(a.elo, b.elo, resultFromScore(...))` and `ENGINE_VERSION`.
  - `GET /healthz` returns 200 `ok`.
  - Any other request returns 404 JSON. A bad body returns 400 JSON.
- Imports `../../js/team.js`, `../../js/engine.js` and `../../js/elo.js`. No copies.

- [ ] **Step 1: Write `sim.test.mjs`** (`node:test`). It starts the server on port 0 and checks:
  - `/play` with `cfgA = { pressDist: 110, possessionPush: 0.16 }`, `cfgB = {}`, seed `12345` and elo 1000/1000 returns exactly the `score` that `simulateMatch(cfgA, cfgB, 12345)` returns in-process, and `deltaA === eloDelta(1000, 1000, resultFromScore(...score))`.
  - `/validate` returns `ok: true` for a house config from `scripts/seed-league.mjs`'s defaults, and `ok: false` with non-empty `errors` for `{formation: "nope"}`.
  - A malformed JSON body gets a 400.
- [ ] **Step 2:** Run `node --test server/sim/sim.test.mjs`. Expected: FAIL.
- [ ] **Step 3:** Implement `sim.mjs` with `node:http`. No dependencies.
- [ ] **Step 4:** Run `node --test server/sim/` and `node test/determinism.test.mjs`. Expected: all pass.
- [ ] **Step 5: Commit.** `git commit -m "Add the sim service that runs the game engine for the API"`

### Task 3: Go API core and auth (Soren writes)

Concepts to teach as you go: Go modules, `net/http` `ServeMux` method patterns
(`"POST /auth/start"`), `context`, pgx pools, `go:embed`, `crypto/rand` + `sha256`,
interfaces for test fakes, and table-driven tests with `httptest`.

**Files:**
- Create: `server/api/go.mod` (module `github.com/iamsorenl/arcade-soccer/server/api`)
- Create: `server/api/main.go` (config from env, wiring, `ListenAndServe`)
- Create: `server/api/app.go`:
  - `type App`
  - `func (a *App) Routes() http.Handler`
  - CORS
  - `writeJSON` / `writeErr`
  - `/healthz`
- Create: `server/api/db.go`: `//go:embed` of `../db/schema.sql` (copy it into `server/api/schema.sql` at build if embed can't reach the parent; the builder picks), plus `ApplySchema(ctx, pool) error`
- Create: `server/api/auth.go`: tokens, sessions, rate limiter, `/auth/*`, `/me`, `requireUser`
- Create: `server/api/mail.go`:
  - `type Mailer interface { SendLogin(ctx context.Context, to, link string) error }`
  - `smtpMailer` (`net/smtp`, STARTTLS)
- Test: `server/api/testutil_test.go`:
  - `newTestApp(t) (*App, *fakeMailer)` resets schema `public` on `TEST_DATABASE_URL` and calls `ApplySchema`
  - `t.Skip` if the env var is unset
- Test: `server/api/auth_test.go`

**Interfaces:**
- Produces:

  ```go
  type Config struct { DatabaseURL, AppURL, SimURL, MailFrom, SMTPHost, SMTPUser, SMTPPass string; SMTPPort int; AllowedOrigins []string; TrustProxy bool; Port string }
  type App struct { DB *pgxpool.Pool; Mail Mailer; Sim Sim; Cfg Config; limit *limiter }
  func (a *App) requireUser(h func(w http.ResponseWriter, r *http.Request, userID string)) http.HandlerFunc
  func clientIP(r *http.Request, trustProxy bool) string // first X-Forwarded-For entry when trustProxy, else RemoteAddr host
  ```

  - `Sim` is defined in Task 6. Until then, a nil field is fine.
- Endpoints:
  - `POST /auth/start {email}` always returns 200 `{"sent": true}` for any valid-looking email, whether or not an account exists. It returns 400 for an empty or `@`-less email, and 429 `too many sign-in emails, try again later` once over the limit (3 per email per 15 min, 10 per IP per hour, in memory). It creates the `users` row on first verify, not here.
  - `POST /auth/verify {token}` → 200 `{"session": "<token>", "user": {"id", "email"}}` | 400 `link expired or already used, request a new one`
  - `POST /auth/signout` (bearer) deletes that session and returns 200.
  - `GET /me` (bearer) → `{"user": {...}, "profile": {"id","username"}|null, "team": {"id","name","elo","version"}|null}` | 401 `not signed in`
  - A session with `expires_at` in the past counts as 401. When `last_seen_at` is older than 24h, set `last_seen_at = now()` and `expires_at = now() + 90 days`.

- [ ] **Step 1: Write the failing tests** in `auth_test.go`:
  - `TestHealthz`: 200.
  - `TestCORS`: `OPTIONS /me` from `https://iamsorenl.github.io` gets that exact `Access-Control-Allow-Origin` and allows the `authorization, content-type` headers. From `https://evil.example` there is no ACAO header.
  - `TestStartSendsLink`: start with `a@b.co` → the fake mailer got one link with prefix `APP_URL + "#login="`.
  - `TestStartNormalizesEmail`: start with `"  A@B.co "`, then verify → `user.email == "a@b.co"`. Pre-insert user `a@b.co` with ID X, and the verify returns ID X.
  - `TestStartRateLimit`: the 4th start for the same email within 15 min → 429.
  - `TestVerifyTokenSingleUse`: verify twice → 200 then 400. The first session still works on `/me`.
  - `TestVerifyExpired`: a token with `expires_at` in the past → 400.
  - `TestMeRequiresSession`: no header, a garbage token, or an expired session → 401.
  - `TestSignout`: after signout, the same token → 401 on `/me`.
- [ ] **Step 2:** Run `cd server/api && TEST_DATABASE_URL=postgres://localhost/league_test go test ./...`. Expected: FAIL (compile errors).
- [ ] **Step 3:** Soren implements `main.go`, `app.go`, `db.go`, `mail.go` and `auth.go`, test by test, with the agent reviewing each one.
- [ ] **Step 4:** Run the tests. Expected: PASS. `go vet ./...` is clean.
- [ ] **Step 5: Commit.** `git commit -m "Add the Go API with magic-link sign-in and sessions"`

### Task 4: Profiles, teams, delete account (Soren writes)

**Files:**
- Create: `server/api/teams.go`
- Test: `server/api/teams_test.go`

**Interfaces:**
- Consumes `requireUser` and `writeErr` from Task 3.
- Endpoints (all bearer):
  - `POST /profile {username}` → 201 `{"id","username"}` | 409 `username taken` | 400 when the length is outside 3–20 | 409 `profile already exists` if the caller has one
  - `PUT /team {name, config, version}` upserts on `owner = userID`, returning `{"id","name","elo","version"}`. 400 when there's no profile yet (`create a profile first`). 400 when `teams_config_sane` or the name length check fails. Elo/W/D/L are never taken from the body.
  - `POST /account/delete {confirm}` → 200 `{"deleted": true, "userId"}` | 400 when `confirm != "DELETE"`. Deletes the `users` row, and the cascade does the rest.

- [ ] **Step 1: Write the failing tests:**
  - `TestCreateProfile`
  - `TestCreateProfileTaken` (second user, same name → 409)
  - `TestPublishTeamUpsert`: publish twice → same `id`, the name changes, `elo` stays 1000.
  - `TestPublishIgnoresElo`: a body with `"elo": 9999` → elo 1000.
  - `TestPublishOtherUsersTeamImpossible`: user B's publish never changes A's row.
  - `TestPublishConfigTooBig`: a 9 KB config → 400.
  - `TestPublishRetryAfterPartial`: create the profile, then call `/profile` again (409 `profile already exists`), then `PUT /team` → 200.
  - `TestDeleteNeedsConfirm`: 400 without confirm.
  - `TestDeleteCascades`: after delete, the profile, team and matches are gone, other users' rows are intact, and the session → 401.
- [ ] **Step 2:** Run `go test ./...`. Expected: FAIL.
- [ ] **Step 3:** Soren implements `teams.go`.
- [ ] **Step 4:** Run `go test ./...`. Expected: PASS.
- [ ] **Step 5: Commit.** `git commit -m "Add profile, team publish and account delete endpoints"`

### Task 5: Leaderboard and matches (Soren writes)

**Files:**
- Create: `server/api/league.go`
- Test: `server/api/league_test.go`

**Interfaces:**
- Public (no auth):
  - `GET /leaderboard?limit=50` → `[{"id","name","elo","wins","draws","losses","config","profiles":{"username"}}]`, Elo descending. No `owner` field. Keep the `profiles` nesting: `league.js` reads it.
  - `GET /matches?team=<uuid>&limit=100` → `[{"id","team_a","team_b","score_a","score_b","elo_delta_a","elo_delta_b","seed","engine_version","config_a","config_b","created_at"}]`, newest first. `team` is optional.
  - `limit` is clamped to 1..200. A non-UUID `team` → 400.

- [ ] **Step 1: Write the failing tests:**
  - `TestLeaderboardOrderAndShape`: 3 teams with different Elo → order, `profiles.username` present, no `owner` key in the raw JSON.
  - `TestMatchesFilter`: a match between A and B plus one between C and D → `?team=A` returns 1.
  - `TestMatchesBadTeam`: 400.
  - `TestLimitClamp`: `limit=100000` → at most 200.
- [ ] **Step 2:** Run. Expected: FAIL.
- [ ] **Step 3:** Soren implements `league.go`.
- [ ] **Step 4:** Run. Expected: PASS.
- [ ] **Step 5: Commit.** `git commit -m "Add leaderboard and match history endpoints"`

### Task 6: Ranked play (Soren writes)

**Files:**
- Create: `server/api/sim.go`:
  - `type Sim interface`
  - `httpSim` client to `SIM_URL`, 5 s timeout
- Create: `server/api/play.go`
- Test: `server/api/play_test.go` (with `fakeSim` in `testutil_test.go`)

**Interfaces:**
- Produces:

  ```go
  type Validation struct { OK bool `json:"ok"`; Errors []string `json:"errors"` }
  type SimTeam struct { Config json.RawMessage `json:"config"`; Elo int `json:"elo"` }
  type SimResult struct { Score [2]int `json:"score"`; DeltaA int `json:"deltaA"`; EngineVersion int `json:"engineVersion"` }
  type Sim interface {
    Validate(ctx context.Context, configs []json.RawMessage) ([]Validation, error)
    Play(ctx context.Context, a, b SimTeam, seed uint32) (SimResult, error)
  }
  ```

- `POST /play {mode, teamId, opponentId?}` (bearer) is a port of `supabase/functions/play-match/index.ts`, with the same statuses and messages:
  - 400 bad mode
  - 404 `team not found`
  - 403 `not your team`
  - 422 `invalid team config` with `details`
  - 400 `challenge needs a different opponentId`
  - 404 `opponent not found`
  - 422 `opponent config is invalid (stale schema?)`
  - 429 `rate limit: 10 matches/hour`
  - 200 `{"mode","matches":[{"opponent","opponentId","seed","score","deltaA"}],"elo","note"?}`
- Seed comes from `crypto/rand` as a uint32. Writes go through `select apply_match_result(..., p_limit => 10)`. A `*pgconn.PgError` with `Code == "PT429"` means capped. Placement matches the five nearest-Elo valid opponents and stops at the cap, with note `stopped at the 10/hour cap`. A `Sim` error → 503 `match server unavailable`.

- [ ] **Step 1: Write the failing tests:**
  - `TestChallengeWritesMatch`: the fake sim returns score [2,1] and deltaA 16 → one `matches` row, A's Elo is 1016, B's is 984, and the response `elo` is 1016.
  - `TestChallengeNotYourTeam`: 403.
  - `TestChallengeSelf`: 400.
  - `TestPlacementFive`: 7 opponents → 5 matches, against the nearest Elo.
  - `TestPlacementStopsAtCap`: pre-insert 8 matches this hour → 2 matches plus the note.
  - `TestCapUnderParallel`: 20 concurrent challenges from one team → exactly 10 `matches` rows.
  - `TestPlaySimDown`: `fakeSim` returns an error → 503, and no rows are written.
- [ ] **Step 2:** Run. Expected: FAIL.
- [ ] **Step 3:** Soren implements `sim.go` and `play.go`.
- [ ] **Step 4:** Run `go test -race ./...`. Expected: PASS.
- [ ] **Step 5:** Local end to end:
  - start `node server/sim/sim.mjs`
  - start the API with `SIM_URL=http://127.0.0.1:8081`
  - sign in two users via `curl` (the dev mailer prints links when `SMTP_HOST` is empty), publish a valid config for each, then `curl` a challenge

  Expected: 200 with a real score.
- [ ] **Step 6: Commit.** `git commit -m "Add ranked play through the sim service"`

### Task 7: Frontend switch-over and maintenance state (Claude builds)

**Files:**
- Create: `js/league-config.js` (`export const LEAGUE_API_URL = 'TODO-...'`, and the `isConfigured()` rule stays "not TODO")
- Modify: `js/net.js` (full rewrite, same exports)
- Modify: `js/league.js:30-34` (`errorText`)
- Delete: `js/supabase-config.js` (at cutover, Task 10, not now). For now, `net.js` picks the new API when `LEAGUE_API_URL` is set.
- Create: `scripts/dev-league.sh` (runs Postgres schema + sim + API locally, prints the URL)
- Test: `test/net.test.mjs`

**Interfaces:**
- Keeps every current `net.js` export and signature:
  - `isConfigured`, `getUser`, `signIn`, `signOut`, `getProfile`, `createProfile`, `publishTeam`
  - `getMyTeam`, `fetchLeaderboard`, `fetchMatches`, `challenge`, `runPlacement`

  The `userId` params become unused (the server takes the user from the session).
- Adds `export class LeagueOffline extends Error {}`, thrown for a network error, the 8000 ms timeout (`AbortSignal.timeout`), or 502/503/504.
- `getUser()` first consumes `#login=<token>` from `location.hash`:
  - POSTs `/auth/verify`
  - stores the session in `localStorage['league.session']` (wrapped in try/catch)
  - clears the hash with `history.replaceState`

  It then calls `/me` and caches the result for `getProfile` / `getMyTeam` in the same page load. A 401 clears the stored session and returns null.
- `errorText(e)` returns the exact maintenance copy when `e instanceof net.LeagueOffline`.

- [ ] **Step 1: Write `test/net.test.mjs`.** It stubs `globalThis.fetch`, `localStorage`, `location` and `history`, then asserts:
  - a hash token gets verified, stored and cleared
  - a 503 → `LeagueOffline`
  - a fetch rejection → `LeagueOffline`
  - a 429 → plain `Error` with the server message
  - a 401 on `/me` → `getUser()` returns null and storage is cleared
- [ ] **Step 2:** Run `node test/net.test.mjs`. Expected: FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run `node test/net.test.mjs` plus the existing tests. Expected: all pass.
- [ ] **Step 5:** Manual run with `scripts/dev-league.sh` and `python3 -m http.server`:
  - sign in (the dev mailer prints the link to stdout when `SMTP_HOST` is empty)
  - publish
  - challenge
  - kill the API → the League shows the maintenance copy, and single player still runs
- [ ] **Step 6: Commit.** `git commit -m "Point the League frontend at the self-hosted API"`

### Task 8: Images and CI (Claude builds)

**Files:**
- Create: `server/api/Dockerfile` (multi-stage, `golang:1.27` → `gcr.io/distroless/static`, `CGO_ENABLED=0`)
- Create: `server/sim/Dockerfile` (`node:24-alpine`, copies `js/` + `server/sim/`, build context = repo root)
- Create: `.github/workflows/league-images.yml`. On push to main touching `server/**` or `js/**`, it runs these against a `postgres:17` service container:
  - `go test ./...`
  - `node --test server/sim/`
  - `node test/*.test.mjs`

  Then it builds and pushes `ghcr.io/iamsorenl/arcade-soccer-api` and `-sim`, tagged `:sha-<short>` and `:latest`.

- [ ] **Step 1:** Run `docker build` for both locally. Expected: both images build.
  - `docker run` on the sim image, then `curl /healthz` → `ok`.
- [ ] **Step 2:** Push the branch and confirm the workflow runs green on a PR (the build-only part runs on PRs; the push to GHCR only runs on main).
- [ ] **Step 3: Commit.** `git commit -m "Build and publish League images from CI"`

### Task 9: Data migration script (Claude builds)

**Files:**
- Create: `scripts/migrate-from-supabase.sh SRC_DUMP_DIR DST_URL`
- Test: run it against `league_test` from Task 0's files

The script:
1. Stops with a list if `auth-users-*.csv` has any two emails equal after `lower(trim())`.
2. Loads `users` from the CSV (normalized emails, same IDs).
3. `pg_restore --data-only -t profiles -t teams -t matches` from the full dump into `DST_URL`.
4. Prints the row counts of the source dump next to the destination's.

- [ ] **Step 1:** Run it against `postgres://localhost/league_test` (schema applied).
  - Expected: counts match for users, profiles, teams and matches, and `exit 0`.
  - The 7 `house-*` teams are present.
- [ ] **Step 2:** Plant a case-duplicate row in a copy of the CSV. Expected: the script stops before loading anything.
- [ ] **Step 3:** Point the local API at that database. `GET /leaderboard` matches the live Supabase leaderboard (names, Elo, W/D/L).
- [ ] **Step 4:** Dev seeding replaces `seed-league.mjs`: `scripts/dev-league.sh --seed <dump dir>` runs this script into the local DB, so dev gets the real house teams. (`seed-league.mjs` targets Supabase's admin API and is removed with `supabase/` in Task 12 Step 5.)
- [ ] **Step 5: Commit.** `git commit -m "Add the Supabase to self-host data migration"`

### Task 10: Server: VM, k3s, Funnel (Soren drives, Claude walks through; ask before starting)

**Files:**
- Create: `deploy/SERVER.md`: the exact commands run, kept as the rebuild runbook

Steps, each run by Soren with an explanation from the agent:
- [ ] **Step 1:** On mce-pc-server:
  - `lxc launch ubuntu:24.04 league --vm -c limits.cpu=4 -c limits.memory=6GiB -d root,size=40GiB`
  - `mkdir -p /srv/league-backups`
  - `lxc config device add league backups disk source=/srv/league-backups path=/backups`

  Expected: `lxc exec league -- df -h /backups` shows the mount.
- [ ] **Step 2:** In the VM: `curl -sfL https://get.k3s.io | INSTALL_K3S_EXEC="--disable traefik" sh -`. Expected: `kubectl get nodes` shows Ready.
- [ ] **Step 3:** Tailnet policy (Soren edits it in the admin console):
  - add `tag:league` with no outbound grants to other nodes
  - add `nodeAttrs` with `"attr": ["funnel"]` for `tag:league`
  - `tailscale up --advertise-tags=tag:league --hostname=league`
- [ ] **Step 4:** `tailscale funnel --bg 30080`. From the laptop (off-tailnet test via phone data): `curl https://league.<tailnet>.ts.net/healthz` reaches the NodePort once Task 11 is deployed.
  - Record the exact URL in `deploy/SERVER.md`.
  - Check whether Funnel sends `X-Forwarded-For` (log one request) and set `TRUST_PROXY` to match.
- [ ] **Step 5: Commit** `deploy/SERVER.md`.

### Task 11: Kubernetes manifests (Soren writes)

Concepts to teach as you go: Namespace, Deployment vs StatefulSet, Service types
(ClusterIP vs NodePort), PersistentVolumeClaim on k3s `local-path`, Secrets, readiness
and liveness probes, CronJob, `kubectl rollout`.

**Files:**
- Create: `deploy/k8s/namespace.yaml` (`league`)
- Create: `deploy/k8s/postgres.yaml`:
  - StatefulSet `postgres:17`, 1 replica, PVC 5Gi
  - ClusterIP Service `postgres:5432`
  - reads Secret `league-db` (`POSTGRES_PASSWORD`)
- Create: `deploy/k8s/sim.yaml`: Deployment (1 replica, `imagePullPolicy: Always`, readiness `GET /healthz`) + ClusterIP Service `sim:8081`
- Create: `deploy/k8s/api.yaml`:
  - Deployment (1 replica, env from ConfigMap `league-api` + Secret `league-api`, readiness `/healthz`)
  - NodePort Service `30080 → 8080`
  - ConfigMap values: `APP_URL=https://iamsorenl.github.io/arcade-soccer/`, `ALLOWED_ORIGINS=https://iamsorenl.github.io`, `SIM_URL=http://sim:8081`, `MAIL_FROM`, `SMTP_HOST=smtp.gmail.com`, `SMTP_PORT=587`, `TRUST_PROXY` (from Task 10)
- Create: `deploy/k8s/backup.yaml`:
  - CronJob at `30 3 * * *`: `pg_dump -Fc` to hostPath `/backups/league-$(date +%F).dump`
  - deletes files older than 14 days
- Secrets are created by hand (documented in `deploy/SERVER.md`, values never in git):
  - `league-db` holds the DB password.
  - `league-api` holds `DATABASE_URL`, `SMTP_USER` (the new game Gmail) and `SMTP_PASS` (its app password).

- [ ] **Step 1:** Soren creates the game Gmail account, turns on 2-step verification and makes the app password.
- [ ] **Step 2:** Soren writes the manifests. Check with `kubectl apply --dry-run=server -f deploy/k8s/`. Expected: no errors.
- [ ] **Step 3:** Apply them. Expected: `kubectl -n league get pods` shows postgres, sim and api Running and Ready, and `curl https://league.<tailnet>.ts.net/healthz` → 200.
- [ ] **Step 4:** Backup drill:
  - `kubectl -n league create job --from=cronjob/backup drill` → a file appears in host `/srv/league-backups`
  - restore it into a scratch database: `pg_restore` into `league_restore_check`
  - check the counts

  Write the restore command in `deploy/SERVER.md`.
- [ ] **Step 5:** Self-test against prod with the frontend pointed at it locally: a real magic link arrives from the game Gmail, then sign in, publish, challenge, delete a throwaway account.
- [ ] **Step 6: Commit.** `git commit -m "Add k8s manifests for the League"`

### Task 12: Cutover (together; ask before starting)

- [ ] **Step 1:** Run Task 0's dump again (fresh data). Run `scripts/migrate-from-supabase.sh` into the prod database (via `kubectl -n league port-forward svc/postgres 15432:5432`). Expected: counts match.
- [ ] **Step 2:** Set `LEAGUE_API_URL` in `js/league-config.js` to the Funnel URL.
  - Delete `js/supabase-config.js` and the supabase-js path in `net.js`.
  - Disable `.github/workflows/keep-supabase-awake.yml` (delete the file).
  - Merge to main so Pages deploys.
- [ ] **Step 3:** On the live site, check:
  - the leaderboard matches the pre-cutover Supabase one
  - Soren signs in with his real email and lands on his old team
  - a challenge works
  - stopping the api Deployment (`kubectl -n league scale deploy/api --replicas=0`) shows the maintenance copy, then scale it back to 1
- [ ] **Step 4:** Update README (the "AI League" section and the `seed-league.mjs` instructions) and PRIVACY.md (data now stored on Soren's server: email, username, team configs). Commit: `git commit -m "Docs: the League now runs on a self-hosted server"`.
- [ ] **Step 5:** Calendar reminder for 2026-11-16 (or 2 weeks after cutover): with Soren's okay, delete the Supabase project. `supabase/` stays in git history only. Remove the folder in a follow-up commit then.
