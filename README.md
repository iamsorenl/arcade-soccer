# Arcade Soccer

A 4v4 top-down arcade soccer game — vanilla JS + Canvas, no dependencies, no build step.

**Play it:** https://iamsorenl.github.io/arcade-soccer/

Single player vs AI (Easy/Normal/Hard) or local 2-player on one keyboard.

## AI League

An AI team is a **config document** run by the shared deterministic engine: formation,
tactical weights, and per-player attribute splits under a shared point budget — no user
code. Build yours in the Team Builder, test it locally (Watch = AI vs AI, Play = you vs it),
then publish it to compete on an Elo ladder. Ranked matches are simulated **server-side**
by a small self-hosted server (`server/`) that runs the very same `js/engine.js` and
`js/team.js` the browser uses, so results are cheat-proof. A match row stores just seed + config snapshots
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

## League server

The League backend lives in `server/` and runs on a home server: k3s in an LXD VM,
reachable publicly through Tailscale Funnel at `https://league.tail88a93f.ts.net`.

- `server/api/` — Go API (stdlib `net/http` + pgx): magic-link sign-in, 90-day sessions,
  profiles, teams, leaderboard, match history, ranked play. Every write acts on the
  signed-in user from the session, never on an id the client sends.
- `server/sim/` — internal Node service that imports `js/engine.js`, `js/team.js` and
  `js/elo.js` unchanged to validate configs and simulate ranked matches.
- `server/db/schema.sql` — Postgres schema. `apply_match_result` writes a match and
  both Elo updates atomically and enforces the 10 matches/hour cap.
- `deploy/k8s/` — manifests; `deploy/SERVER.md` — the runbook (setup, deploy, logs,
  backups, restore).

`js/league-config.js` holds the API URL (keep the CSP `connect-src` in `index.html` in
sync). If the server is down, the League shows "under maintenance" and the rest of the
game keeps working.

Local dev against a migrated copy of the data:

```sh
scripts/dev-league.sh --seed ~/Backups/arcade-soccer   # Postgres + sim + API on :8080
```

Server tests: `cd server && TEST_DATABASE_URL=... go test -race -p 1 ./...` and
`node --test server/sim/sim.test.mjs`. CI runs them all
(`.github/workflows/league-images.yml`) and publishes the api/sim images to GHCR on
pushes to main.

The seven house teams (`scripts/house-teams.mjs`) are bot accounts with `house-*`
usernames and undeliverable emails, so the ladder never looks empty.

## License

MIT — see [LICENSE](LICENSE).

## Privacy

See [PRIVACY.md](PRIVACY.md) for what data the app stores and who sees it.

### Credits

Player sprites from the [Kenney Sports Pack](https://opengameart.org/content/sports-pack-350) (CC0).
