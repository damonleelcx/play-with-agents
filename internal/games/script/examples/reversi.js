// Reversi: place a disc so that it outflanks a straight line of your
// opponent's discs; every outflanked disc flips to your colour. A player
// with no capturing move must pass; the game ends when neither can move.
// Most discs wins.
//
// Shows: a forced "pass" move, highlighting the mover's legal cells with
// `mark`, a richer view (scores, counters) and a `heuristic`, which lets the
// AI players stop random playouts early and evaluate the position instead.

const N = 8;
// Direction vectors as two flat arrays. The helpers below use plain indexed
// loops on purpose: they run thousands of times per AI decision, and the
// sandboxed interpreter is far slower than a browser engine at destructuring
// and iterator-based loops.
const DR = [-1, -1, -1, 0, 0, 1, 1, 1];
const DC = [-1, 0, 1, -1, 1, -1, 0, 1];

// Positional weights: corners are permanent, the squares next to them hand
// the corner to the opponent, edges are fairly stable.
const WEIGHTS = [
  [100, -20, 10, 5, 5, 10, -20, 100],
  [-20, -50, -2, -2, -2, -2, -50, -20],
  [10, -2, 1, 1, 1, 1, -2, 10],
  [5, -2, 1, 0, 0, 1, -2, 5],
  [5, -2, 1, 0, 0, 1, -2, 5],
  [10, -2, 1, 1, 1, 1, -2, 10],
  [-20, -50, -2, -2, -2, -2, -50, -20],
  [100, -20, 10, 5, 5, 10, -20, 100],
];

// How many opponent discs `seat` would outflank from (r, c) in direction d.
function run(board, seat, r, c, d) {
  const opp = 1 - seat;
  let rr = r + DR[d], cc = c + DC[d], n = 0;
  while (rr >= 0 && rr < N && cc >= 0 && cc < N && board[rr][cc] === opp) {
    rr += DR[d];
    cc += DC[d];
    n++;
  }
  const closed = rr >= 0 && rr < N && cc >= 0 && cc < N && board[rr][cc] === seat;
  return n > 0 && closed ? n : 0;
}

function canPlace(board, seat, r, c) {
  if (board[r][c] !== null) return false;
  for (let d = 0; d < 8; d++) if (run(board, seat, r, c, d) > 0) return true;
  return false;
}

// Places a disc and flips the outflanked lines in place; returns the flips.
function place(board, seat, r, c) {
  let flipped = 0;
  for (let d = 0; d < 8; d++) {
    const n = run(board, seat, r, c, d);
    for (let k = 1; k <= n; k++) board[r + DR[d] * k][c + DC[d] * k] = seat;
    flipped += n;
  }
  board[r][c] = seat;
  return flipped;
}

function placements(board, seat) {
  const out = [];
  for (let r = 0; r < N; r++) {
    for (let c = 0; c < N; c++) {
      if (canPlace(board, seat, r, c)) out.push([r, c]);
    }
  }
  return out;
}

// Like placements(board, seat).length > 0, but stops at the first one.
function hasPlacement(board, seat) {
  for (let r = 0; r < N; r++) {
    for (let c = 0; c < N; c++) {
      if (canPlace(board, seat, r, c)) return true;
    }
  }
  return false;
}

function counts(board) {
  const n = [0, 0];
  for (const row of board) for (const v of row) if (v !== null) n[v]++;
  return n;
}

const game = {
  meta: {
    name: "Reversi",
    summary: "Outflank and flip your opponent's discs. Most discs wins.",
    minSeats: 2,
    maxSeats: 2,
    hiddenInfo: false,
    turnSeconds: 45,
    options: {},
  },

  setup(ctx) {
    const board = [];
    for (let r = 0; r < N; r++) board.push(Array(N).fill(null));
    board[3][3] = 1; board[4][4] = 1;
    board[3][4] = 0; board[4][3] = 0;
    return { board, turn: 0, over: false, last: null };
  },

  toMove(state) {
    return state.over ? [] : [state.turn];
  },

  legal(state, seat) {
    if (state.over || seat !== state.turn) return [];
    const spots = placements(state.board, seat);
    // A player with no capture must pass (apply ends the game when the
    // opponent cannot move either, so a pass is never followed by a pass).
    if (spots.length === 0) return [{ type: "pass", label: "Pass (no legal placement)" }];
    return spots.map(([r, c]) => ({
      type: "place",
      label: `${"abcdefgh"[c]}${r + 1}`,
      args: { r, c },
      ui: { cell: [r, c] },
    }));
  },

  apply(state, seat, move) {
    const board = state.board.map((row) => row.slice());
    const events = [];
    let last = null;
    if (move.type === "place") {
      const { r, c } = move.args;
      const flipped = place(board, seat, r, c);
      last = [r, c];
      events.push({
        type: "place", seat,
        text: `{s:${seat}} plays ${"abcdefgh"[c]}${r + 1} and flips ${flipped}`,
        data: { flipped },
      });
    } else {
      events.push({ type: "pass", seat, text: `{s:${seat}} has no move and passes` });
    }

    // The opponent always moves next. If they have no placement they get a
    // single "pass" move, so everyone sees the pass in the log. When neither
    // side can place, the game is over (so a pass is never answered by a pass).
    const opp = 1 - seat;
    const turn = opp;
    const over = !hasPlacement(board, opp) && !hasPlacement(board, seat);
    if (over) {
      const [a, b] = counts(board);
      events.push({ type: "end", seat, text: a === b ? `Neither player can move: ${a}–${b}, a draw` : `Neither player can move: {s:${a > b ? 0 : 1}} wins ${Math.max(a, b)}–${Math.min(a, b)}` });
    }
    return { state: { board, turn, over, last }, events };
  },

  view(state, seat) {
    // Show the player to move where they can play (only on their own screen).
    const showHints = !state.over && seat === state.turn;
    const cells = state.board.map((row, r) => row.map((owner, c) => {
      const cell = {};
      if (owner !== null) cell.piece = { shape: "disc", color: `p${owner}` };
      if (showHints && canPlace(state.board, seat, r, c)) cell.mark = "#ffffff22";
      else if (state.last && state.last[0] === r && state.last[1] === c) cell.mark = "#ffd16655";
      return Object.keys(cell).length ? cell : null;
    }));
    const [a, b] = counts(state.board);
    let message;
    if (state.over) message = a === b ? `Draw, ${a}–${b}` : `{s:${a > b ? 0 : 1}} wins ${Math.max(a, b)}–${Math.min(a, b)}`;
    else message = `{s:${state.turn}} to move`;
    return {
      title: "Reversi",
      board: { rows: N, cols: N, style: "checker", cells },
      players: [{ seat: 0, score: a, color: "p0" }, { seat: 1, score: b, color: "p1" }],
      counters: [{ label: "Empty squares", value: N * N - a - b }],
      message,
    };
  },

  outcome(state) {
    if (!state.over) return null;
    const [a, b] = counts(state.board);
    const rank = a === b ? [1, 1] : a > b ? [1, 2] : [2, 1];
    const summary = a === b ? `Draw, ${a}–${b}` : `{s:${a > b ? 0 : 1}} wins ${Math.max(a, b)}–${Math.min(a, b)}`;
    return { rank, score: [a, b], summary };
  },

  // How good is the position for `seat`, in [-1, 1]? Early on, position and
  // mobility matter; disc count only matters near the end (a classic
  // beginner mistake is to grab discs early).
  heuristic(state, seat) {
    const opp = 1 - seat;
    const [a, b] = counts(state.board);
    const mine = seat === 0 ? a : b, theirs = seat === 0 ? b : a;
    if (state.over) return Math.sign(mine - theirs);

    let positional = 0;
    for (let r = 0; r < N; r++) {
      for (let c = 0; c < N; c++) {
        const v = state.board[r][c];
        if (v === seat) positional += WEIGHTS[r][c];
        else if (v === opp) positional -= WEIGHTS[r][c];
      }
    }
    const myMoves = placements(state.board, seat).length;
    const theirMoves = placements(state.board, opp).length;
    const mobility = (myMoves - theirMoves) / (myMoves + theirMoves + 1);
    const progress = (mine + theirs) / (N * N);
    const material = (mine - theirs) / (mine + theirs);
    return Math.tanh(positional / 80 + mobility + material * progress * progress * 2);
  },
};
