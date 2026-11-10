# Privacy

_Last updated: 2026-11-10_

Arcade Soccer is a hobby project built and run by one person, Soren Larsen — not
a company. Here's what the app does with your data.

## Signing in

Sign-in is a magic link sent to your email from playarcadesoccer@gmail.com. The League
server, which Soren runs on his own computer at home, stores that email address to
send the link and keep you signed in. No passwords are stored.
Your email is never shown to other users, and no query the app makes ever selects
another user's email.

## What's public

If you publish a team, these are visible to anyone on the internet, no login required:
your leaderboard username, your team name, your team's config (the AI settings you
built), your Elo rating, your win/draw/loss record, and your full match history. Don't
put anything in your username or team name you don't want public.

## What's stored locally

Your browser's `localStorage` holds your in-progress team draft (`arcade-soccer-team`)
and your League session token (it keeps you signed in for up to 90 days). Both stay on your device — clearing your browser
data clears them.

## No tracking

There are no cookies, no analytics, no ad trackers, and no third-party tracking scripts
of any kind. There's nothing here to consent to.

## Third parties involved

- **Gmail** — sends the magic-link email from the game's own Gmail account.
- **Tailscale** — carries traffic between the internet and the League server.
- **GitHub Pages** — hosts the static site, and sees standard web server logs (IP
  address, user agent) like any web host does.

None of these are ad or tracking services — they're infrastructure the app runs on.

## How long data is kept

Your account, team and matches: until you delete them. Nightly database backups are
kept for 14 days. Unused sign-in links expire after 15 minutes; sessions after 90 days
without use.

## Deleting your data

Email **iamsorenl@gmail.com** and ask for your account to be deleted. That removes your
email, profile, team and all your match records. Copies in backups age out
within 14 days.

## Questions

Email iamsorenl@gmail.com.
