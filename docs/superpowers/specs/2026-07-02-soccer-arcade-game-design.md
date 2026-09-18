# Soccer Arcade Game — Design

**Date:** 2026-07-02
**Status:** Approved
**Hosting:** GitHub Pages at `https://iamsorenl.github.io/arcade-soccer/`

## Overview

A top-down 2D arcade soccer game, playable in the browser. 4v4 (3 outfield
players + 1 goalkeeper per side), timed matches, single-player vs AI or local
2-player on one keyboard. Built with vanilla JavaScript and HTML5 Canvas — no
dependencies, no build step — so GitHub Pages serves the repo root directly.

## Goals

- A fun, responsive arcade soccer game anyone can play from a URL.
- Single-player vs AI (three difficulties) and local 2-player modes.
- Zero build/deploy machinery: push to `main`, Pages serves it.

## Non-goals (v1)

- Online multiplayer, sound, mobile/touch controls, throw-ins/corners/fouls,
  tackling button, sprint/stamina, team customization, persistent stats.

## Architecture

Static site at the repo root, plain ES modules (`<script type="module">`):

```
index.html      — canvas element, menu overlay, score/clock HUD
css/style.css   — page layout, menu styling
js/main.js      — entry point: game loop, match state machine
js/config.js    — all tuning constants (speeds, friction, pitch size, match length)
js/input.js     — keyboard handling (two independent control schemes)
js/physics.js   — circle movement, friction, ball–player and ball–wall collisions
js/entities.js  — Player, Ball, Team definitions
js/ai.js        — teammate positioning, opponent team brain, goalkeeper logic
js/render.js    — draws pitch, players, ball, effects on the canvas
```

- **Game loop:** fixed-timestep simulation at 60 updates/sec, rendering
  decoupled via `requestAnimationFrame`, with an accumulator. Large frame gaps
  (e.g. tab switch) are clamped so physics cannot explode.
- **Match state machine:** `menu → kickoff → playing → goal → halftime →
  playing → fulltime`, plus a paused state (Esc).
- Physics functions are kept pure (state in, state out) so they can be
  sanity-checked in isolation.

## Match rules

- Horizontally-oriented enclosed pitch (indoor-soccer style walls), goals as
  openings in the left and right walls. Ball fully crossing the line = goal.
- Two 2-minute halves; clock in the HUD; teams swap ends at halftime.
- Kickoff from center at the start of each half and after every goal, with a
  1-second countdown freeze.
- Highest score wins; draws are allowed and reported as draws.

## Gameplay mechanics

- **Ball physics:** circle with velocity and friction; bounces off boundary
  walls.
- **Dribbling:** a player close to the ball nudges it ahead of them as they
  move. Opponents win the ball by touching it — positioning, not a tackle
  button.
- **Passing:** finds the best teammate in the mover's direction (nearest
  forward teammate if neutral) and kicks the ball to their feet with a small
  lead.
- **Shooting:** hold to charge (longer = harder, capped), release to shoot
  toward the opponent goal; movement direction bends the aim.
- **Player switching:** you always control your team's player nearest the
  ball, marked with a ring, with hysteresis so control doesn't flicker
  between equidistant players.

## Controls

| Action | Player 1 | Player 2 |
|--------|----------|----------|
| Move   | WASD     | Arrow keys |
| Pass   | C        | , (comma) |
| Shoot (hold to charge) | V | . (period) |
| Pause  | Esc      | Esc |

## AI

- **Teammates:** hold formation positions that shift with the ball; push up
  in possession, drop back defending; stay open for passes rather than
  ball-chasing.
- **Opponent team brain:** each tick assigns roles — nearest presses the ball
  carrier, one covers the passing lane, the rest keep formation. In
  possession: dribble toward goal, pass when pressed, shoot inside range with
  aim error.
- **Difficulty (Easy/Normal/Hard):** scales AI speed, reaction delay, and
  shot accuracy. Chosen on the menu for single-player.
- **Goalkeepers:** always AI on both sides. Track the ball's y-position along
  the goal line, come out to smother nearby loose balls, clear upfield to a
  teammate.

## Menus & HUD

- Start screen: "1 Player" (with difficulty), "2 Players", controls legend.
- In-match: kickoff countdown, goal banner pause, halftime banner, full-time
  result screen with "Play again".
- HUD bar: score, clock, half indicator.

## Rendering

Flat-color style on canvas: green pitch with line markings, players as
colored circles (blue vs red) with direction indicator and control ring,
white ball with subtle shadow. Canvas scales to fit the window preserving
aspect ratio.

## Error handling & testing

No network, storage, or free-form input — small risk surface. Frame-gap
clamping protects the simulation. Testing is manual browser playtesting;
pure physics functions allow isolated sanity checks.

## Deployment

Push to `main`; enable GitHub Pages (deploy from branch `main`, root `/`).
Live at `https://iamsorenl.github.io/arcade-soccer/`.
