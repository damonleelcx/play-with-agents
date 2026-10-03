// Tic-tac-toe: the smallest complete game module, and the template the
// others follow.
//
// The contract in one paragraph: the module defines a global `game`. The
// runtime calls its functions with plain JSON values and stores whatever
// setup/apply return, so the state must be plain JSON (no Map, Set, class
// instances or functions). Every function must be pure: never keep anything
// in module-level variables between calls, and never use Math.random or Date
// (they do not exist here; use ctx.random()). Text shown to players refers
// to seats as {s:N}; the platform substitutes the player's name.

// The eight winning lines, as indices into the 3x3 board stored row-major.
const LINES = [
  [0, 1, 2], [3, 4, 5], [6, 7, 8], // rows
  [0, 3, 6], [1, 4, 7], [2, 5, 8], // columns
  [0, 4, 8], [2, 4, 6],            // diagonals
];
const MARKS = ["X", "O"];

function winnerOf(cells) {
  for (const [a, b, c] of LINES) {
    if (cells[a] !== null && cells[a] === cells[b] && cells[a] === cells[c]) return cells[a];
  }
  return null;
}

function isOver(state) {
  return state.winner !== null || state.cells.every((c) => c !== null);
}

const game = {
  meta: {
    name: "Tic-tac-toe",
    summary: "Three in a row on a 3×3 grid. X moves first.",
    minSeats: 2,
    maxSeats: 2,
    hiddenInfo: false,
    turnSeconds: 20,
    options: {},
  },

  // State: cells holds the seat that owns each square (or null), row-major.
  setup(ctx) {
    return { cells: Array(9).fill(null), turn: 0, winner: null };
  },

  // Seats that must act now; [] once the game is over.
  toMove(state) {
    return isOver(state) ? [] : [state.turn];
  },

  // One move per empty square. The ui hint lets the board renderer turn a
  // click on that cell into this move; the label is used by agents and
  // screen readers.
  legal(state, seat) {
    if (isOver(state) || seat !== state.turn) return [];
    const moves = [];
    state.cells.forEach((c, i) => {
      if (c !== null) return;
      const r = Math.floor(i / 3), col = i % 3;
      moves.push({ type: "place", label: `Row ${r + 1}, column ${col + 1}`, args: { cell: i }, ui: { cell: [r, col] } });
    });
    return moves;
  },

  // The runtime has already checked the move against legal(), so apply only
  // has to play it. Returning { state, events } feeds the table log.
  apply(state, seat, move) {
    const i = move.args.cell;
    const cells = state.cells.slice();
    cells[i] = seat;
    const winner = winnerOf(cells);
    const next = { cells, turn: 1 - seat, winner };

    const events = [{
      type: "place", seat,
      text: `{s:${seat}} plays ${MARKS[seat]} at row ${Math.floor(i / 3) + 1}, column ${(i % 3) + 1}`,
    }];
    if (winner !== null) events.push({ type: "win", seat, text: `{s:${seat}} completes a line and wins` });
    else if (isOver(next)) events.push({ type: "draw", seat, text: "The board is full: it's a draw" });
    return { state: next, events };
  },

  // Perfect information: every seat (and spectators, seat -1) sees the same
  // board. Pieces use the player colours p0/p1.
  view(state, seat) {
    const cells = [];
    for (let r = 0; r < 3; r++) {
      const row = [];
      for (let c = 0; c < 3; c++) {
        const owner = state.cells[r * 3 + c];
        row.push(owner === null ? null : { piece: { shape: "text", glyph: MARKS[owner], color: `p${owner}` } });
      }
      cells.push(row);
    }
    let message;
    if (state.winner !== null) message = `{s:${state.winner}} wins!`;
    else if (isOver(state)) message = "Draw";
    else message = `{s:${state.turn}} to play ${MARKS[state.turn]}`;
    return {
      title: "Tic-tac-toe",
      board: { rows: 3, cols: 3, style: "grid", cells },
      players: [0, 1].map((s) => ({ seat: s, info: MARKS[s], color: `p${s}` })),
      message,
    };
  },

  // null while playing; otherwise rank (1 = winner, ties share) and score
  // with one entry per seat.
  outcome(state) {
    if (!isOver(state)) return null;
    if (state.winner === null) return { rank: [1, 1], score: [0.5, 0.5], summary: "Draw" };
    const w = state.winner;
    return {
      rank: w === 0 ? [1, 2] : [2, 1],
      score: w === 0 ? [1, 0] : [0, 1],
      summary: `{s:${w}} wins with three in a row`,
    };
  },
};
