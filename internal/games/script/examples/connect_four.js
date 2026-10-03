// Connect Four: drop discs into a 7-column, 6-row grid; four in a row wins.
//
// Shows: gravity (a move is a column, the disc lands on the lowest free
// row), win detection from the last move only, and ui hints that point at
// the landing cell so the client can preview the drop.

const ROWS = 6;
const COLS = 7;
const DIRS = [[0, 1], [1, 0], [1, 1], [1, -1]]; // →, ↓, ↘, ↙ (and their opposites)

// Lowest empty row in column c, or -1 if the column is full. Row 0 is the top.
function landingRow(board, c) {
  for (let r = ROWS - 1; r >= 0; r--) if (board[r][c] === null) return r;
  return -1;
}

// Does the disc just placed at (r, c) complete four in a row?
function makesFour(board, r, c) {
  const who = board[r][c];
  for (const [dr, dc] of DIRS) {
    let run = 1;
    for (const sign of [1, -1]) {
      let rr = r + dr * sign, cc = c + dc * sign;
      while (rr >= 0 && rr < ROWS && cc >= 0 && cc < COLS && board[rr][cc] === who) {
        run++;
        rr += dr * sign;
        cc += dc * sign;
      }
    }
    if (run >= 4) return true;
  }
  return false;
}

const game = {
  meta: {
    name: "Connect Four",
    summary: "Drop discs, line up four.",
    minSeats: 2,
    maxSeats: 2,
    hiddenInfo: false,
    turnSeconds: 30,
    options: {},
  },

  setup(ctx) {
    const board = [];
    for (let r = 0; r < ROWS; r++) board.push(Array(COLS).fill(null));
    return { board, turn: 0, winner: null, moves: 0, last: null };
  },

  toMove(state) {
    if (state.winner !== null || state.moves === ROWS * COLS) return [];
    return [state.turn];
  },

  legal(state, seat) {
    if (game.toMove(state)[0] !== seat) return [];
    const moves = [];
    for (let c = 0; c < COLS; c++) {
      const r = landingRow(state.board, c);
      if (r >= 0) moves.push({ type: "drop", label: `Column ${c + 1}`, args: { col: c }, ui: { cell: [r, c] } });
    }
    return moves;
  },

  apply(state, seat, move) {
    const c = move.args.col;
    const r = landingRow(state.board, c);
    // Copy only the row that changes; the other rows are shared, which is
    // fine because the runtime hands every call a fresh copy anyway.
    const board = state.board.slice();
    board[r] = board[r].slice();
    board[r][c] = seat;

    const won = makesFour(board, r, c);
    const next = { board, turn: 1 - seat, winner: won ? seat : null, moves: state.moves + 1, last: [r, c] };
    const events = [{ type: "drop", seat, text: `{s:${seat}} drops a disc in column ${c + 1}` }];
    if (won) events.push({ type: "win", seat, text: `{s:${seat}} connects four!` });
    else if (next.moves === ROWS * COLS) events.push({ type: "draw", seat, text: "The grid is full: it's a draw" });
    return { state: next, events };
  },

  view(state, seat) {
    const cells = state.board.map((row, r) => row.map((owner, c) => {
      if (owner === null) return null;
      const cell = { piece: { shape: "disc", color: `p${owner}` } };
      // Highlight the most recent disc so players can follow the game.
      if (state.last && state.last[0] === r && state.last[1] === c) cell.mark = "#ffffff55";
      return cell;
    }));
    let message;
    if (state.winner !== null) message = `{s:${state.winner}} connects four!`;
    else if (state.moves === ROWS * COLS) message = "Draw: the grid is full";
    else message = `{s:${state.turn}} to drop a disc`;
    return {
      title: "Connect Four",
      board: { rows: ROWS, cols: COLS, style: "grid", cells },
      players: [0, 1].map((s) => ({ seat: s, color: `p${s}` })),
      counters: [{ label: "Discs played", value: state.moves }],
      message,
    };
  },

  outcome(state) {
    if (game.toMove(state).length > 0) return null;
    if (state.winner === null) return { rank: [1, 1], score: [0.5, 0.5], summary: "Draw: the grid is full" };
    const w = state.winner;
    return {
      rank: w === 0 ? [1, 2] : [2, 1],
      score: w === 0 ? [1, 0] : [0, 1],
      summary: `{s:${w}} connects four in ${state.moves} moves`,
    };
  },

  // On timeout, play the most central open column: rarely a blunder.
  defaultMove(state, seat) {
    for (const c of [3, 2, 4, 1, 5, 0, 6]) {
      if (landingRow(state.board, c) >= 0) return { type: "drop", args: { col: c } };
    }
    return null;
  },
};
