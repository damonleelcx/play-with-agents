// Comet Run: a race on a map board — the reference for games whose board is
// not a grid (tracks, routes, islands, networks of places).
//
// A map board is a list of spaces placed anywhere (x, y in percent of the
// board), links drawn between them and regions painted under them. Moves
// point at spaces by id: {space:"s4"} to click one, {from:"s4", to:"n2"} to
// move a piece. Several pieces may share a space (pieces: [...]).

// The track: 20 spaces on an oval around the planet, clockwise from the
// dock (s0), plus a nebula shortcut from s5 to s12 through n1..n3.
const TRACK = 20;
const SPACES = (() => {
  const out = [];
  for (let i = 0; i < TRACK; i++) {
    const a = -Math.PI / 2 + (2 * Math.PI * i) / TRACK;
    out.push({ id: "s" + i, x: 50 + 42 * Math.cos(a), y: 50 + 40 * Math.sin(a) });
  }
  out.push({ id: "n1", x: 64, y: 40 }, { id: "n2", x: 58, y: 54 }, { id: "n3", x: 48, y: 66 });
  return out;
})();
const NEXT = (() => {
  const next = {};
  for (let i = 0; i < TRACK; i++) next["s" + i] = ["s" + ((i + 1) % TRACK)];
  next.s5 = ["s6", "n1"]; // the fork: the long way round, or the nebula
  next.n1 = ["n2"];
  next.n2 = ["n3"];
  next.n3 = ["s12"];
  return next;
})();
// Steps from each space back to the dock by the shortest route (the AI's
// sense of who is ahead).
const TO_DOCK = (() => {
  const d = {};
  for (let i = 0; i < TRACK; i++) d["s" + i] = i === 0 ? TRACK : TRACK - i;
  d.s5 = Math.min(d.s5, 4 + d.s12);
  d.n1 = 3 + d.s12; d.n2 = 2 + d.s12; d.n3 = 1 + d.s12;
  return d;
})();
const SHIPS = 2;
// Each player holds thrusters 1-4, spends one per turn and gets all four
// back once they are used up: every move is a choice, not a roll.
const THRUST = [1, 2, 3, 4];

// Every space a ship at `from` can end on after exactly `steps` steps (a
// fork offers both ways). Passing the dock finishes the lap: "home".
function destinations(from, steps) {
  let frontier = [from];
  for (let k = 0; k < steps; k++) {
    const next = [];
    for (const p of frontier) {
      if (p === "home") continue;
      for (const n of NEXT[p]) {
        const q = n === "s0" ? "home" : n;
        if (!next.includes(q)) next.push(q);
      }
    }
    frontier = next;
  }
  return frontier;
}

function done(state, seat) {
  return state.ships[seat].every((p) => p === "home");
}

// Steps a fleet still has to fly (a ship home counts zero).
function left(state, s) {
  return state.ships[s].reduce((n, p) => n + (p === "home" ? 0 : TO_DOCK[p]), 0);
}

// The race ends when a fleet is home, or after ROUNDS rounds; then the fleet
// with the fewest steps left wins (ties: the earlier seat in turn order).
const ROUNDS = 60;

function winner(state) {
  for (let s = 0; s < state.ships.length; s++) if (done(state, s)) return s;
  if (state.moves < ROUNDS * state.ships.length) return null;
  let best = 0;
  for (let s = 1; s < state.ships.length; s++) if (left(state, s) < left(state, best)) best = s;
  return best;
}

const game = {
  meta: {
    name: "Comet Run",
    summary: "Race two ships around the planet; cut through the nebula, bump rivals back to the dock.",
    minSeats: 2,
    maxSeats: 4,
    hiddenInfo: false,
    turnSeconds: 25,
    options: {},
  },

  // ships[seat][i] is a space id, or "home" once that ship finished its lap.
  setup(ctx) {
    const ships = [];
    for (let s = 0; s < ctx.seats; s++) ships.push(Array(SHIPS).fill("s0"));
    return { ships, thrust: ships.map(() => THRUST.slice()), turn: 0, moves: 0 };
  },

  toMove(state) {
    return winner(state) === null ? [state.turn] : [];
  },

  // One move per (ship space, destination), with the thruster that gets it
  // there: two ships on the same space offer the same moves once. If no
  // ship can fly, the only move is to pass.
  legal(state, seat) {
    if (winner(state) !== null || seat !== state.turn) return [];
    const moves = [];
    const seen = {};
    for (const from of state.ships[seat]) {
      if (from === "home") continue;
      for (const t of state.thrust[seat]) for (const to of destinations(from, t)) {
        const k = from + ">" + to;
        if (seen[k]) continue;
        seen[k] = true;
        moves.push({
          type: "fly", label: `Thrust ${t}: ${from} → ${to}`, args: { from, to, thrust: t },
          // Finishing has no space to click on the board: the dock stands in.
          ui: { from, to: to === "home" ? "s0" : to },
        });
      }
    }
    if (moves.length === 0) moves.push({ type: "pass", label: "No ship can fly: pass", args: {} });
    return moves;
  },

  apply(state, seat, move, ctx) {
    const ships = state.ships.map((a) => a.slice());
    const events = [];
    if (move.type === "fly") {
      const { from, to } = move.args;
      const i = ships[seat].indexOf(from);
      ships[seat][i] = to;
      if (to === "home") {
        events.push({ type: "home", seat, text: `{s:${seat}} brings a ship home` });
      } else {
        // A lone rival ship on the landing space is bumped back to the dock.
        for (let s = 0; s < ships.length; s++) {
          if (s === seat) continue;
          const here = ships[s].filter((p) => p === to).length;
          if (here === 1) {
            ships[s][ships[s].indexOf(to)] = "s0";
            events.push({ type: "bump", seat, text: `{s:${seat}} bumps {s:${s}} back to the dock` });
          }
        }
        if (to.startsWith("n") && !from.startsWith("n")) {
          events.push({ type: "nebula", seat, text: `{s:${seat}} dives into the nebula` });
        }
      }
    } else {
      events.push({ type: "pass", seat, text: `{s:${seat}} has no ship that can fly` });
    }
    const thrust = state.thrust.map((a) => a.slice());
    if (move.type === "fly") {
      thrust[seat].splice(thrust[seat].indexOf(move.args.thrust), 1);
      if (thrust[seat].length === 0) thrust[seat] = THRUST.slice();
    }
    const next = { ships, thrust, turn: (seat + 1) % ships.length, moves: state.moves + 1 };
    if (winner(next) !== null) events.push({ type: "win", seat, text: `{s:${seat}} wins the Comet Run` });
    return { state: next, events };
  },

  view(state, seat) {
    const spaces = SPACES.map((sp) => {
      const pieces = [];
      state.ships.forEach((own, s) => own.forEach((p) => {
        if (p === sp.id) pieces.push({ shape: "ship", color: `p${s}` });
      }));
      const out = { id: sp.id, x: sp.x, y: sp.y, shape: sp.id.startsWith("n") ? "star" : "circle" };
      if (sp.id === "s0") Object.assign(out, { label: "Dock", shape: "hex", size: 1.6 });
      if (sp.id === "s5") out.label = "Fork";
      if (pieces.length) out.pieces = pieces;
      return out;
    });
    const links = [];
    for (const [from, tos] of Object.entries(NEXT)) {
      for (const to of tos) links.push({ from, to, style: from.startsWith("n") || to.startsWith("n") ? "dashed" : "arrow" });
    }
    const w = winner(state);
    return {
      title: "Comet Run",
      board: {
        theme: "space",
        aspect: 1.25,
        spaces,
        links,
        regions: [
          { x: 34, y: 34, w: 32, h: 32, shape: "ellipse", color: "#3b5bdb", label: "Planet Vesta" },
          { x: 44, y: 32, w: 28, h: 40, shape: "blob", color: "#9c36b5", label: "Nebula" },
        ],
      },
      players: state.ships.map((own, s) => ({
        seat: s, color: `p${s}`, score: own.filter((p) => p === "home").length, info: `thrusters ${state.thrust[s].join(" ")}`,
      })),
      counters: [{ label: "Thrusters", value: state.thrust[state.turn].join(" · ") }],
      message: w !== null ? `{s:${w}} wins the Comet Run` : `{s:${state.turn}} to fly: thrusters ${state.thrust[state.turn].join(", ")}`,
      prompt: "Pick a ship, then where it lands (each landing uses its thruster)",
    };
  },

  outcome(state) {
    const w = winner(state);
    if (w === null) return null;
    const home = state.ships.map((own) => own.filter((p) => p === "home").length);
    return {
      rank: home.map((h, s) => (s === w ? 1 : 2)),
      score: state.ships.map((own, s) => 2 * TRACK - left(state, s)),
      summary: `{s:${w}} wins the Comet Run`,
    };
  },

  // Closer to the dock is better; a ship home counts as zero steps left.
  heuristic(state, seat) {
    const mine = left(state, seat);
    let best = Infinity;
    for (let s = 0; s < state.ships.length; s++) if (s !== seat) best = Math.min(best, left(state, s));
    return Math.max(-1, Math.min(1, (best - mine) / (2 * TRACK)));
  },
};
