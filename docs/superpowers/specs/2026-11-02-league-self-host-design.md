# AI League off Supabase, onto the home server

Date: 2026-11-02. Status: draft for Soren's review.

## Why

The AI League's Supabase project (`opiallhyhgfjlsfgzmmm`, free tier) keeps getting
paused for inactivity. The GitHub keepalive (`keep-supabase-awake.yml`) gets HTTP 200
from the `keepalive()` write every other day, and Supabase still sent a pause warning on
2026-11-02, so tuning the ping isn't a fix. When the project pauses, nobody can sign in,
publish, or play ranked, and being able to log in and compete is the whole point of the
League.

The move is also a learning project. Go and Kubernetes are gaps on Soren's resume
(Go appears in 48 of his target postings, Kubernetes in 70), and he picked them on
2026-10-25 as things he learns himself rather than has handed to him.

## Goals

- The League (sign-in, publish, placement, challenges, leaderboard, match history,
  delete account) runs on Soren's home server with no free-tier pausing.
- Sign-ins last: once someone is signed in, they stay signed in for months on that
  browser, not hours.
- All existing data comes across: accounts (same user IDs), profiles, teams with their
  Elo and W/D/L, and match history.
- If the server is down, the League says it's under maintenance. The game (single
  player, 2-player, team builder, local Watch/Play) keeps working, as it does today.
- Soren writes the Go API and the Kubernetes manifests himself, with Claude explaining
  and reviewing. That's where the learning is.

## Non-goals

- High availability, multiple nodes, failover. Downtime is acceptable (Soren's call).
- Porting the match engine to Go. The engine stays the one JavaScript copy.
- Social login (Google/GitHub). Magic link stays the only sign-in method.
- Terraform, AWS, or a cloud VM. Possible later; not part of this.
- Changing gameplay, Elo math, rate limits, or the UI beyond the maintenance state.

## Anti-goals (would be a failure even if it "works")

- Losing or resetting anyone's team, Elo, or match history in the cutover.
- Any League problem breaking the offline game.
- Exposing anything else on the server or tailnet (WarmPath Postgres, Ollama, other LXD
  containers) to the internet.
- Weaker security than today: today a user can only edit their own profile/team
  columns, only the server writes Elo and matches, the hourly match cap can't be beaten
  with parallel requests, and delete-account needs `{confirm: "DELETE"}` and only ever
  deletes the caller. All of that must still hold.

## Decided

**Where it runs (Soren):** on `mce-pc-server`. Downtime is fine; the frontend shows a
maintenance message.

**Learning split (Soren, 2026-10-25):** Soren writes the Go and Kubernetes parts with
Claude teaching and reviewing. Claude builds the plumbing: the `js/net.js` rewrite, the
sim worker wrapper, the data migration script, CI.

## The shape (Claude's calls unless marked)

### Host

- A new **LXD VM** named `league` on mce-pc-server (`lxc launch --vm`; the host has the
  qemu driver, 16 cores, 30 GB RAM). Not a container: k3s needs a real kernel, and a VM
  keeps it apart from `nightly-soren`, which holds WarmPath secrets.
- **k3s**, single node, inside that VM.
- Housemates have sudo on the host. Accepted: the League stores only emails, usernames,
  and team configs. No passwords.

### Public URL

- **Tailscale Funnel** on the VM, giving a public `https://league.<tailnet>.ts.net`.
  Tailscale is already on the server. A Cloudflare Tunnel with a named hostname would
  mean moving larsensoren.com's DNS off Google Domains first, which isn't worth it for
  an API URL nobody types.
- The VM joins the tailnet with its own tag (e.g. `tag:league`) that can't reach other
  tailnet nodes. Funnel exposes only the API's port.
- The frontend stays on GitHub Pages, unchanged except for `js/net.js` and the
  maintenance message.

### Workloads (three, in k3s)

1. **`api`, Go.** Standard library `net/http` plus a Postgres driver (pgx). It's the only
   thing Funnel reaches. It handles:
   - `POST /auth/start {email}` sends the magic link
   - `POST /auth/verify {token}` returns a session token
   - `POST /auth/signout`
   - `GET /me` returns user, profile, and own team
   - `POST /profile {username}`
   - `PUT /team {name, config, version}` upserts the caller's one team
   - `GET /leaderboard`
   - `GET /matches?team=`
   - `POST /play {mode, teamId, opponentId?}`
   - `POST /account/delete {confirm}`
   - `GET /healthz`

   Response shapes match what `league.js` reads today (field names in `js/net.js`), so
   `league.js` barely changes.
2. **`sim`, Node.** A small internal-only HTTP service that imports `js/team.js`,
   `js/engine.js`, and `js/elo.js` unchanged and exposes validate and simulate as pure
   functions: configs plus a seed in, result out. It never touches the database. The Go
   API does what `play-match/index.ts` does today: picks opponents, calls `sim`, then
   writes through `apply_match_result` with the hourly cap of 10.
3. **`postgres`.** One instance on a k3s local-path volume.

### Database

- Keep the current schema and **keep `apply_match_result` as is**: the atomic insert,
  the Elo update, and the advisory-lock rate limit already live in Postgres, and moving
  them into Go would bring back the parallel-request bug 0002 fixed.
- Supabase-specific parts go: `auth.users`, RLS policies, and `anon`/`authenticated`
  grants. Their job moves into the Go handlers. Each write handler takes the user ID
  from the session only, never from the request body, which is the same rule
  delete-account follows today.
- New tables:
  - `users (id uuid pk, email unique, created_at)`. `profiles.id` references it with
    on-delete cascade, so delete-account still cascades users → profiles → teams →
    matches.
  - `login_tokens (token_hash, email, expires_at, used_at)`: 15-minute, single-use links.
  - `sessions (token_hash, user_id, created_at, last_seen_at, expires_at)`: 90-day
    expiry, extended on use. Only hashes are stored.
- Drop `keepalive`.

### Sign-in

- Magic link as today. The email links back to the game page with `#login=<token>`, and
  `net.js` posts it to `/auth/verify` and stores the session token.
- The session token lives in `localStorage` and is sent as a bearer header. Not a cookie:
  the page (github.io) and the API (ts.net) are different sites, and browsers block
  cross-site cookies. supabase-js stores its session the same way.
- Rate limits on `/auth/start` per email and per client IP. The builder should confirm
  how Funnel passes the client IP.
- Email sending: **SMTP through a Gmail app password** for a sending account (Claude's
  default). No DNS setup. Gmail's ~500/day cap is far above League traffic. Open: which
  address strangers see as the sender (see Open questions).
- CORS allows only `https://iamsorenl.github.io` (plus localhost for dev).

### Maintenance state

- `net.js` treats a network error, a timeout (about 8s), or a 502/503/504 as "League
  offline". `league.js` then shows: "The AI League is under maintenance. The rest of the
  game still works. Check back soon." It shows this instead of an error, in the League
  view and on publish.
- Real errors (400s, rate limit, validation) still show their messages as today.

### Backups

- A nightly `pg_dump` CronJob writes to a directory on the host disk, outside the VM
  (an LXD disk device), keeping 14 days. One documented restore command, tested once
  before cutover.

### Build and deploy

- Go API and `sim` live in this repo (`server/api/`, `server/sim/`). `sim` imports
  `../../js/*` the same way the Edge Function does.
- Plain Kubernetes YAML in `deploy/k8s/`. No Helm or Kustomize unless it earns its place.
- GitHub Actions builds both images to GHCR on push to main (the repo is public), and the
  VM pulls them. Deploy is `kubectl apply` or a rollout restart. The builder picks
  whether the VM pulls on a timer or Soren runs one command.
- Secrets (DB password, SMTP password) are Kubernetes Secrets created by hand on the VM,
  never in the repo.

### Cutover

1. Run everything in parallel against a copy of the data loaded from a fresh Supabase
   dump, while Supabase stays live.
2. Short freeze. Dump `public.*` plus `auth.users (id, email)` from Supabase and load it,
   keeping the IDs.
3. Switch `net.js` to the new API and deploy Pages. Check sign-in, publish, a challenge,
   the leaderboard, and delete-account on a throwaway account, against the live API.
4. Disable `keep-supabase-awake.yml`. Leave the Supabase project alone for 2 weeks as a
   rollback, then pause or delete it with Soren's okay.
5. Update README (the League section says Supabase Edge Function) and PRIVACY.md (who
   stores what now).

Existing users sign in again once after cutover. Their accounts, teams, and Elo are kept.

### Checks

- Go tests for the trust-boundary parts:
  - a user can't touch another user's team
  - `/play` respects the cap under parallel requests
  - delete-account needs the confirm string and only deletes the caller
  - expired or used login tokens fail
- `sim` parity: for a fixed set of configs and seeds, `sim` returns exactly what
  `js/engine.js` returns in the existing `test/determinism.test.mjs` setup.
- Existing `test/*.test.mjs` keep passing.

## Settled after the builder check (Claude's calls)

- **Data insurance first.** Step 0 of the plan is a full `pg_dump` of the Supabase
  database, taken now, before any build work. Soren copies the database password from
  the Supabase dashboard once. If the project pauses mid-build, unpause it from the
  dashboard (allowed for 90 days, no data lost) to take the cutover dump.
- **Order:** local-only work comes first: Go API against local Postgres, `sim`, and the
  `net.js` rewrite. Server work comes after: VM, k3s, Funnel. The server was up on
  2026-11-02 (16 cores, 30 GB RAM, 1.1 TB free, LXD with qemu).
- **Tailnet:** Soren is the tailnet admin. Adding `tag:league` and the `funnel` attribute
  is a policy change he approves when the plan gets there. The API base URL lives in one
  config constant (as `supabase-config.js` does today), so a later URL change is a
  one-line frontend redeploy.
- **House teams:** the 7 `house-*` accounts migrate as ordinary rows with their
  `.invalid` emails. They can never sign in, because no link can be delivered.
  `seed-league.mjs` gets ported to write directly to the new database for dev setups.
- **Publish validation:** keep today's behavior (8 KB size fence on publish, full
  `validateTeam` at `/play`). `sim` stays off the publish path. Stricter validation is a
  possible follow-up, not part of this move.
- **Email matching:** emails are stored trimmed and lowercased. The migration script
  checks for case-only duplicates before loading and stops if it finds any.

- **Accepted tradeoff: links are not bound to the browser that asked for them.** A
  link requested on the laptop can be opened on the phone. The flip side: someone could
  send a person a sign-in link for the sender's own account (login CSRF). The impact is
  small here, since the League shows the signed-in email and an account holds only a
  game team. Binding links to the requesting browser (a nonce in localStorage) would
  fix it, at the cost of cross-device sign-in. Revisit if accounts ever hold anything
  more valuable.

## Left to the builder

The Go package layout, the router (standard library is fine), the migration tool or plain
SQL files, the sim RPC shape, how images get onto the VM, the CronJob details, how to
seed a dev database, and the order of tasks. Plan it as a sequence Soren can learn from:
small working steps, each one explained.

## Sender account (Claude's recommendation, 2026-11-02)

Magic-link emails come from a **new Gmail account made for the game** (e.g.
`arcadesoccer.league@gmail.com`), not Soren's personal Gmail. A Gmail app password can
also read the inbox over IMAP, and it will sit in a k3s Secret on a host where housemates
have root. Spam reports and lockouts also land on the throwaway account. Soren creates
the account, turns on 2-step verification, and makes the app password when the plan gets
to email.
