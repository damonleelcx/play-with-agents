// Story Tiles: a storytelling tile game for 2 players with hidden hands.
//
// Twelve unique story cards (characters, places, events, twists). Each has a
// name, one line of story and an effect. Both players hold 3 cards; 6 wait
// in the deck. On your turn, play one card from your hand onto an empty
// square of the 5×5 board, then draw. The story so far is the story lines of
// the cards played, in order. When all 12 cards are played, every card
// scores its value plus its effect, for whoever owns it then.
//
// Shows: rich cards (title, text, effect, kind, value), "card → cell" moves
// ({zone, index, cell}), cards on the board (cell.card), sealed squares
// (cell.blocked), the story panel (view.story), a hand prompt, hidden hands
// with `determinize`, and a heuristic computed from the public board.

const SIZE = 5;
const HAND = 3;

// Effects are data: `adj` scores per orthogonally adjacent card of a kind
// ("any" counts every card, "mine"/"theirs" by owner); `after` scores when
// the previous story card has that kind; `edge` scores on the board's rim;
// `seal` seals the empty squares around it; `claim` takes the adjacent
// opponent card of lowest value.
const CARDS = [
  { id: "lamplighter", kind: "character", title: "The Lamplighter", value: 1,
    text: "Every dusk, the old lamplighter climbed the hill with a ladder and one match.",
    effect: "+1 for each adjacent place.", fx: { adj: "place", per: 1 } },
  { id: "fox", kind: "character", title: "A Fox in a Red Coat", value: 1,
    text: "A fox in a red coat followed him, asking questions nobody could answer.",
    effect: "+2 for each adjacent event.", fx: { adj: "event", per: 2 } },
  { id: "queen", kind: "character", title: "The Exiled Queen", value: 2,
    text: "Somewhere below, an exiled queen counted the lights as they came on.",
    effect: "+1 for each adjacent card you own.", fx: { adj: "mine", per: 1 } },
  { id: "library", kind: "place", title: "The Drowned Library", value: 1,
    text: "The library had sunk into the lake, but its windows still glowed.",
    effect: "+1 for each adjacent character.", fx: { adj: "character", per: 1 } },
  { id: "bridge", kind: "place", title: "Bridge of Paper Cranes", value: 1,
    text: "A bridge of paper cranes held for exactly one crossing each night.",
    effect: "+2 on an edge square.", fx: { edge: 2 } },
  { id: "market", kind: "place", title: "The Night Market", value: 0,
    text: "At the night market, people traded secrets for warm bread.",
    effect: "+1 for each adjacent card.", fx: { adj: "any", per: 1 } },
  { id: "storm", kind: "event", title: "A Storm Rolls In", value: 2,
    text: "Then the storm came, and every road but one washed away.",
    effect: "Seals the empty squares around it.", fx: { seal: true } },
  { id: "bells", kind: "event", title: "The Bells at Midnight", value: 1,
    text: "At midnight all the bells rang, though no one had touched the ropes.",
    effect: "+1 for each adjacent character.", fx: { adj: "character", per: 1 } },
  { id: "letter", kind: "event", title: "A Letter Arrives", value: 1,
    text: "A letter arrived, addressed to someone who had not been born yet.",
    effect: "+3 if the story card before it is a character.", fx: { after: "character", bonus: 3 } },
  { id: "dream", kind: "twist", title: "But It Was a Dream", value: 0,
    text: "But it was a dream, or so they agreed, and nobody believed it.",
    effect: "Claim the adjacent opponent card of lowest value.", fx: { claim: true } },
  { id: "map", kind: "twist", title: "The Map Was Upside Down", value: 1,
    text: "The map had been upside down all along; north was a door.",
    effect: "+2 if the story card before it is a place.", fx: { after: "place", bonus: 2 } },
  { id: "listener", kind: "twist", title: "Someone Was Listening", value: 1,
    text: "And someone, behind the wall, had been listening the whole time.",
    effect: "+1 for each adjacent opponent card.", fx: { adj: "theirs", per: 1 } },
];

const byId = (id) => CARDS.find((c) => c.id === id);
const DIRS = [[-1, 0], [0, -1], [0, 1], [1, 0]]; // reading order: claim ties go to the first
const ROWS = "ABCDE";
const squareName = (r, c) => `${ROWS[r]}${c + 1}`;

function neighbours(r, c) {
  return DIRS.map(([dr, dc]) => [r + dr, c + dc]).filter(([y, x]) => y >= 0 && y < SIZE && x >= 0 && x < SIZE);
}

// The story position of every played card, to resolve "after" effects.
function storyIndex(state) {
  const at = {};
  state.story.forEach((s, i) => { at[s.id] = i; });
  return at;
}

// Points of the card on (r, c) for its owner, on the current board.
function cardPoints(state, r, c, at) {
  const cell = state.board[r][c];
  const card = byId(cell.id);
  const fx = card.fx;
  let pts = card.value;
  if (fx.adj) {
    for (const [y, x] of neighbours(r, c)) {
      const n = state.board[y][x];
      if (!n || !n.id) continue;
      const ok = fx.adj === "any" || (fx.adj === "mine" && n.seat === cell.seat) ||
        (fx.adj === "theirs" && n.seat !== cell.seat) || byId(n.id).kind === fx.adj;
      if (ok) pts += fx.per;
    }
  }
  if (fx.edge && (r === 0 || c === 0 || r === SIZE - 1 || c === SIZE - 1)) pts += fx.edge;
  if (fx.after) {
    const i = at[cell.id];
    if (i > 0 && byId(state.story[i - 1].id).kind === fx.after) pts += fx.bonus;
  }
  return pts;
}

function scores(state) {
  const out = [0, 0];
  const at = storyIndex(state);
  for (let r = 0; r < SIZE; r++) for (let c = 0; c < SIZE; c++) {
    const cell = state.board[r][c];
    if (cell && cell.id) out[cell.seat] += cardPoints(state, r, c, at);
  }
  return out;
}

const openSquares = (state) => {
  const out = [];
  for (let r = 0; r < SIZE; r++) for (let c = 0; c < SIZE; c++) if (state.board[r][c] === null) out.push([r, c]);
  return out;
};

const isOver = (state) => state.hands[state.turn].length === 0 || openSquares(state).length === 0;

// The card as the renderer shows it.
function face(card, seat) {
  const out = { title: card.title, text: card.text, effect: card.effect, kind: card.kind, value: card.value };
  if (seat !== undefined) out.seat = seat;
  return out;
}

const game = {
  meta: {
    name: "Story Tiles",
    summary: "Play story cards onto a 5×5 board and tell a tale together; effects score for whoever owns the card.",
    minSeats: 2,
    maxSeats: 2,
    hiddenInfo: true,
    turnSeconds: 60,
    options: {},
  },

  // board[r][c] is null (open), { sealed: true } or { id, seat };
  // story lists { id, seat } in play order (seat: who played it).
  setup(ctx) {
    const deck = CARDS.map((c) => c.id);
    ctx.shuffle(deck);
    const hands = [deck.splice(0, HAND), deck.splice(0, HAND)];
    const board = [];
    for (let r = 0; r < SIZE; r++) board.push(Array(SIZE).fill(null));
    return { board, hands, deck, story: [], turn: ctx.randomInt(2), over: false };
  },

  toMove(state) {
    return state.over ? [] : [state.turn];
  },

  // One move per (card, open square). The ui hint names the card in the
  // hand zone and the cell, so the board plays "card, then square".
  legal(state, seat) {
    if (state.over || seat !== state.turn) return [];
    const moves = [];
    const open = openSquares(state);
    state.hands[seat].forEach((id, i) => {
      const card = byId(id);
      for (const [r, c] of open) {
        moves.push({
          type: "play",
          label: `${card.title} → ${squareName(r, c)}`,
          args: { card: id, r, c },
          ui: { zone: `hand-${seat}`, index: i, cell: [r, c] },
        });
      }
    });
    return moves;
  },

  apply(state, seat, move, ctx) {
    const { card: id, r, c } = move.args;
    const card = byId(id);
    state.hands[seat] = state.hands[seat].filter((x) => x !== id);
    state.board[r][c] = { id, seat };
    state.story.push({ id, seat });
    const events = [{ type: "play", seat, text: `{s:${seat}} plays “${card.title}” on ${squareName(r, c)}: ${card.text}` }];

    if (card.fx.seal) {
      let n = 0;
      for (const [y, x] of neighbours(r, c)) if (state.board[y][x] === null) { state.board[y][x] = { sealed: true }; n++; }
      if (n) events.push({ type: "seal", seat, text: `The storm seals ${n} square${n === 1 ? "" : "s"}` });
    }
    if (card.fx.claim) {
      let best = null;
      for (const [y, x] of neighbours(r, c)) {
        const t = state.board[y][x];
        if (t && t.id && t.seat !== seat && (best === null || byId(t.id).value < byId(state.board[best[0]][best[1]].id).value)) best = [y, x];
      }
      if (best) {
        const t = state.board[best[0]][best[1]];
        state.board[best[0]][best[1]] = { id: t.id, seat };
        events.push({ type: "claim", seat, text: `{s:${seat}} claims “${byId(t.id).title}”` });
      }
    }

    if (state.deck.length > 0) {
      const drawn = state.deck.shift();
      state.hands[seat].push(drawn);
      events.push({ type: "draw", seat, text: `{s:${seat}} draws a card` });
      events.push({ type: "drew", seat, text: `You drew “${byId(drawn).title}”`, only: [seat] });
    }

    state.turn = 1 - seat;
    if (isOver(state)) state.over = true;
    return { state, events };
  },

  // The board, the story and the scores are public. Each seat sees its own
  // hand; the other hand and the deck are backs only.
  view(state, seat) {
    const cells = state.board.map((row) => row.map((cell) => {
      if (cell === null) return null;
      if (cell.sealed) return { blocked: true };
      return { card: face(byId(cell.id), cell.seat) };
    }));
    const zones = [0, 1].map((p) => ({
      id: `hand-${p}`,
      label: p === seat ? "Your story cards" : "Story cards",
      owner: p,
      layout: p === seat ? "fan" : "row",
      cards: state.hands[p].map((id) => (p === seat ? face(byId(id)) : { hidden: true })),
    }));
    zones.push({ id: "deck", label: "Deck", layout: "stack", cards: state.deck.map(() => ({ hidden: true })) });

    const sc = scores(state);
    let message;
    if (state.over) {
      message = sc[0] === sc[1] ? `The tale ends in a tie at ${sc[0]}` : `{s:${sc[0] > sc[1] ? 0 : 1}} tells the better tale, ${Math.max(...sc)} to ${Math.min(...sc)}`;
    } else {
      message = `{s:${state.turn}} continues the story`;
    }
    const v = {
      title: "Story Tiles",
      board: { rows: SIZE, cols: SIZE, style: "tiles", cells },
      zones,
      story: state.story.map((s) => ({ title: byId(s.id).title, text: byId(s.id).text, seat: s.seat })),
      players: [0, 1].map((p) => ({ seat: p, score: sc[p], info: `${state.hands[p].length} in hand`, color: `p${p}` })),
      counters: [{ label: "Cards played", value: `${state.story.length} / ${CARDS.length}` }],
      message,
    };
    if (!state.over && seat === state.turn) v.prompt = "Choose a card, then a square, to continue the story";
    return v;
  },

  outcome(state) {
    if (!state.over) return null;
    const sc = scores(state);
    const rank = sc.map((s) => 1 + sc.filter((t) => t > s).length);
    const summary = sc[0] === sc[1]
      ? `{s:0} and {s:1} tell an equal tale, ${sc[0]} each`
      : `{s:${sc[0] > sc[1] ? 0 : 1}} wins the tale, ${Math.max(...sc)} to ${Math.min(...sc)}`;
    return { rank, score: sc, summary };
  },

  // Everything seat cannot see: the other hand and the deck order. Card ids
  // are pooled and redealt with the same hand size.
  determinize(state, seat, ctx) {
    const other = 1 - seat;
    const pool = state.hands[other].concat(state.deck);
    ctx.shuffle(pool);
    state.hands[other] = pool.splice(0, state.hands[other].length);
    state.deck = pool;
    return state;
  },

  heuristic(state, seat) {
    const sc = scores(state);
    return Math.max(-1, Math.min(1, (sc[seat] - sc[1 - seat]) / 15));
  },
};
