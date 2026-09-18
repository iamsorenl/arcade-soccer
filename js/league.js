// AI League screen: sign-in, leaderboard, match history, replays, and the
// publish flow used by the builder. All data comes through net.js; replays
// re-run the deterministic sim client-side from the stored seed + configs.

import { ENGINE_VERSION } from './engine.js';
import { validateTeam } from './team.js';
import * as net from './net.js';

const $ = (id) => document.getElementById(id);

// Tiny DOM helper: user text always goes through textContent, never markup.
function el(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

let user = null;   // signed-in Supabase user (or null)
let myTeam = null; // the user's published team row (or null)

function setStatus(text, isError = false) {
  const box = $('league-status');
  box.textContent = text || '';
  box.classList.toggle('league-status-error', Boolean(text) && isError);
}

// A dead/unreachable backend surfaces as a bare "TypeError: Failed to fetch",
// which tells a player nothing. Everything else is already a real message.
function errorText(e) {
  return /failed to fetch|networkerror|load failed/i.test(e.message)
    ? "Can't reach the league server — check your connection; the backend may be down."
    : e.message;
}

// ---------- Auth area ----------

async function renderAuth() {
  const box = $('league-auth');
  box.replaceChildren();
  user = await net.getUser();
  myTeam = user ? await net.getMyTeam(user.id) : null;

  if (user) {
    box.append(el('span', 'league-user', user.email));
    const out = el('button', 'btn', 'Sign Out');
    out.addEventListener('click', async () => {
      await net.signOut();
      await refresh();
    });
    box.append(out);
    return;
  }

  const notice = el(
    'div',
    'league-loading',
    'Signing in and publishing a team makes your username, team, rating, and match history public to anyone. '
      + 'Your email is only used to send the sign-in link and is never shown to other users. '
  );
  const privacyLink = document.createElement('a');
  // GitHub's rendered view, not the relative path: .nojekyll means Pages
  // serves raw markdown, which browsers download instead of displaying.
  privacyLink.href =
    'https://github.com/iamsorenl/arcade-soccer/blob/main/PRIVACY.md';
  privacyLink.textContent = 'Privacy notice';
  privacyLink.target = '_blank';
  privacyLink.rel = 'noopener';
  notice.append(privacyLink);
  box.append(notice);

  const email = el('input', 'league-email');
  email.type = 'email';
  email.placeholder = 'you@example.com';
  const send = el('button', 'btn', 'Send Sign-In Link');
  send.addEventListener('click', async () => {
    try {
      await net.signIn(email.value.trim());
      setStatus('Check your email for the sign-in link, then reopen this page.');
    } catch (e) {
      setStatus(errorText(e), true);
    }
  });
  box.append(email, send);
}

// ---------- Leaderboard ----------

// Last-5 form string ("WWDLL") for a team from the recent-matches pull.
function formFor(teamId, matches) {
  const letters = [];
  for (const m of matches) {
    if (m.team_a !== teamId && m.team_b !== teamId) continue;
    const us = m.team_a === teamId ? m.score_a : m.score_b;
    const them = m.team_a === teamId ? m.score_b : m.score_a;
    letters.push(us > them ? 'W' : us < them ? 'L' : 'D');
    if (letters.length === 5) break;
  }
  return letters.join('') || '—';
}

async function renderBoard() {
  const box = $('league-board');
  box.replaceChildren(el('div', 'league-loading', 'Loading…'));
  const [teams, recent] = await Promise.all([
    net.fetchLeaderboard(),
    net.fetchMatches(null, 200),
  ]);

  const table = el('table', 'league-table');
  const head = el('tr');
  for (const h of ['#', 'Team', 'Owner', 'Elo', 'W-D-L', 'Form', '']) {
    head.append(el('th', null, h));
  }
  table.append(head);

  teams.forEach((t, i) => {
    const row = el('tr');
    row.append(el('td', null, String(i + 1)));

    // Team name opens its match history / scouting view.
    const nameCell = el('td');
    const nameBtn = el('button', 'league-link', t.name);
    nameBtn.addEventListener('click', () => renderHistory(t));
    nameCell.append(nameBtn);
    row.append(nameCell);

    row.append(el('td', null, (t.profiles && t.profiles.username) || '?'));
    row.append(el('td', null, String(t.elo)));
    row.append(el('td', null, `${t.wins}-${t.draws}-${t.losses}`));
    row.append(el('td', 'league-form', formFor(t.id, recent)));

    const actCell = el('td');
    // Anyone can play any published team — unranked scouting/fun.
    const play = el('button', 'btn league-challenge', 'Play');
    play.addEventListener('click', () => {
      if (!validateTeam(t.config).ok) {
        setStatus(`${t.name}'s saved team config is invalid or from an incompatible version — can't start that match.`, true);
        return;
      }
      $('league').classList.add('hidden');
      onPlayCb(t.config, t.name);
    });
    actCell.append(play);
    if (user && myTeam && t.id !== myTeam.id) {
      const btn = el('button', 'btn league-challenge', 'Challenge');
      btn.addEventListener('click', async () => {
        btn.disabled = true;
        setStatus(`Challenging ${t.name}…`);
        try {
          const res = await net.challenge(myTeam.id, t.id);
          const m = res.matches[0];
          setStatus(
            `${myTeam.name} ${m.score[0]} – ${m.score[1]} ${t.name}  (Elo ${m.deltaA >= 0 ? '+' : ''}${m.deltaA})`
          );
          await renderBoard(); // ratings moved
        } catch (e) {
          setStatus(errorText(e), true);
          btn.disabled = false;
        }
      });
      actCell.append(btn);
    }
    row.append(actCell);
    table.append(row);
  });

  box.replaceChildren(table);
  if (teams.length === 0) box.replaceChildren(el('div', 'league-loading', 'No teams published yet.'));
}

// ---------- Match history + replay ----------

let onReplayCb = null;
let onPlayCb = null;

async function renderHistory(team) {
  const box = $('league-history');
  box.classList.remove('hidden');
  box.replaceChildren(el('div', 'league-loading', `Loading ${team.name}…`));

  const matches = await net.fetchMatches(team.id, 20);
  const title = el('div', 'league-history-title', `${team.name} — recent matches`);
  const table = el('table', 'league-table');
  const head = el('tr');
  for (const h of ['When', 'Match', 'Elo', '']) head.append(el('th', null, h));
  table.append(head);

  for (const m of matches) {
    const row = el('tr');
    row.append(el('td', null, new Date(m.created_at).toLocaleString()));
    row.append(
      el('td', null, `${m.config_a.name} ${m.score_a} – ${m.score_b} ${m.config_b.name}`)
    );
    const delta = m.team_a === team.id ? m.elo_delta_a : m.elo_delta_b;
    row.append(el('td', null, `${delta >= 0 ? '+' : ''}${delta}`));

    // Replays are free: same engine, same configs, same seed. The stored
    // score lets the viewer flag a diverged replay instead of lying.
    const cell = el('td');
    const replay = el('button', 'btn league-challenge', 'Replay');
    replay.addEventListener('click', () => {
      if (!validateTeam(m.config_a).ok || !validateTeam(m.config_b).ok) {
        setStatus("This match's stored team config is invalid or from an incompatible version — can't replay it.", true);
        return;
      }
      $('league').classList.add('hidden');
      onReplayCb(
        m.config_a, m.config_b, m.seed,
        m.engine_version !== ENGINE_VERSION,
        [m.score_a, m.score_b]
      );
    });
    cell.append(replay);
    row.append(cell);
    table.append(row);
  }
  if (matches.length === 0) {
    const row = el('tr');
    row.append(el('td', null, 'No matches yet.'));
    table.append(row);
  }

  box.replaceChildren(title, table);
}

// ---------- Publish flow (called from the builder) ----------

// Publishes the local team doc, then runs placement matches. Returns a
// one-line status string for the builder to display; throws on failure.
export async function publishFlow(team) {
  if (!net.isConfigured()) {
    throw new Error('Online league not configured (fill in js/supabase-config.js).');
  }
  const u = await net.getUser();
  if (!u) throw new Error('Sign in from the League screen first.');

  // First publish needs a leaderboard username.
  let profile = await net.getProfile(u.id);
  if (!profile) {
    const username = (prompt('Pick a leaderboard username (3-20 chars):') || '').trim();
    profile = await net.createProfile(u.id, username);
  }

  const row = await net.publishTeam(u.id, team);
  const placed = await net.runPlacement(row.id);
  if (placed.matches.length === 0) {
    return `Published "${team.name}" (Elo ${placed.elo}) — no opponents yet.`;
  }
  const record = placed.matches
    .map((m) => `${m.score[0]}-${m.score[1]} vs ${m.opponent}`)
    .join(', ');
  return `Published! Placement: ${record}. Elo ${placed.elo}.`;
}

// ---------- Wiring ----------

export function initLeague({ onReplay, onPlay }) {
  onReplayCb = onReplay;
  onPlayCb = onPlay;

  $('btn-league').addEventListener('click', async () => {
    $('league').classList.remove('hidden');
    await refresh();
  });
  $('btn-league-back').addEventListener('click', () => {
    $('league').classList.add('hidden');
    $('league-history').classList.add('hidden');
  });
}

async function refresh() {
  setStatus('');
  $('league-history').classList.add('hidden');
  if (!net.isConfigured()) {
    $('league-auth').replaceChildren();
    $('league-board').replaceChildren();
    setStatus('Online league not configured yet — fill in js/supabase-config.js.');
    return;
  }
  try {
    await renderAuth();
    await renderBoard();
  } catch (e) {
    // Clear the board: a failed load must not sit under "Loading…" forever.
    $('league-board').replaceChildren();
    setStatus(errorText(e), true);
  }
}
