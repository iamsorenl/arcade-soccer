# Privacy

_Last updated: 2026-09-18_

Arcade Soccer is a hobby project built and run by one person, Soren Larsen — not
a company. Here's what the app does with your data.

## Signing in

Sign-in is a magic link sent to your email. Supabase (the backend this project uses)
stores that email address in its auth system to send the link and keep you signed in.
Your email is never shown to other users, and no query the app makes ever selects
another user's email.

## What's public

If you publish a team, these are visible to anyone on the internet, no login required:
your leaderboard username, your team name, your team's config (the AI settings you
built), your Elo rating, your win/draw/loss record, and your full match history. Don't
put anything in your username or team name you don't want public.

## What's stored locally

Your browser's `localStorage` holds your in-progress team draft (`arcade-soccer-team`)
and your Supabase auth session token. Both stay on your device — clearing your browser
data clears them.

## No tracking

There are no cookies, no analytics, no ad trackers, and no third-party tracking scripts
of any kind. There's nothing here to consent to.

## Third parties involved

- **Supabase** — database, authentication, and sends the magic-link email.
- **GitHub Pages** — hosts the static site, and sees standard web server logs (IP
  address, user agent) like any web host does.
- **esm.sh** — a CDN that serves one JavaScript library to your browser when the page
  loads.

None of these are ad or tracking services — they're infrastructure the app runs on.

## How long data is kept

Indefinitely. There's no automatic expiry or cleanup job.

## Deleting your data

Email **iamsorenl@gmail.com** and ask for your account to be deleted. That removes your
profile, your team, and all your match records. An in-app "delete my account" button is
planned but not built yet — email is the only way to do it right now.

## Questions

Email iamsorenl@gmail.com.
