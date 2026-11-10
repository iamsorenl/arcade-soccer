// The seven house teams that populate the AI League ladder: the four builder
// presets plus Tiki-Taka, Counter Attack and Sunday League. Their owners are
// bot accounts named house-* with undeliverable *.invalid emails, so nobody can
// sign in as them. They already live in the League database (migrated from the
// old Supabase backend); test/team.test.mjs keeps every config valid.

import { FORMATIONS, defaultTeam } from '../js/team.js';

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
