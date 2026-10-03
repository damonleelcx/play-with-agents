// Lantern Market: a set-collection card game for 2–4 players with hidden
// hands.
//
// 35 lantern cards in five colours (7 each). Four lie face up in the market.
// On your turn do one thing:
//   • Take a market lantern (everyone sees what you took), or
//   • Draw blind from the deck (only you see it), or
//   • Sell every lantern of one colour in your hand: n lanterns score
//     1+2+…+n (1, 3, 6, 10, 15, 21). The first player to sell 3 or more of a
//     colour also wins that colour's festival bonus, +3.
// A hand holds at most 6 lanterns; with 6 you must sell. When the deck and
// the market are both empty, every other player gets one last call to sell,
// then the game ends and each unsold lantern costs 1 point.
//
// Shows: hidden information (blind draws are hidden from opponents, market
// takes are public), private events with `only`, `determinize` for the AI
// players, card zones with ui hints, and a custom `defaultMove`.

const COLOURS = [
  { name: "Crimson", hex: "#e5484d" },
  { name: "Amber", hex: "#f5b83d" },
  { name: "Jade", hex: "#2fbf8f" },
  { name: "Azure", hex: "#3e8ef7" },
  { name: "Violet", hex: "#a46ef0" },
];
const PER_COLOUR = 7;
const MARKET_SIZE = 4;
const HAND_LIMIT = 6;
const START_HAND = 2;
const FESTIVAL_BONUS = 3;
const FESTIVAL_MIN = 3;

const setValue = (n) => (n * (n + 1)) / 2;
const supplyEmpty = (state) => state.deck.length === 0 && state.market.length === 0;
const countOf = (hand, colour) => hand.filter((card) => card.c === colour).length;
const plural = (n, word) => `${n} ${word}${n === 1 ? "" : "s"}`;

// A hand sorted by colour: the order the owner sees, and the order the ui
// hints of "sell" moves index into.
const sortedHand = (hand) => hand.slice().sort((x, y) => x.c - y.c);

function refillMarket(state) {
  while (state.market.length < MARKET_SIZE && state.deck.length > 0) state.market.push(state.deck.pop());
}

function nextSeat(state, seat) {
  return (seat + 1) % state.hands.length;
}

const game = {
  meta: {
    name: "Lantern Market",
    summary: "Collect lanterns, sell sets before the market closes. Hidden hands, 2–4 players.",
    minSeats: 2,
    maxSeats: 4,
    hiddenInfo: true,
    turnSeconds: 40,
    options: {},
  },

  // Hands hold { c: colour, seen: bool }. `seen` marks cards taken face up
  // from the market: opponents know them, so they are shown face up in the
  // opponents' view and kept fixed by determinize.
  setup(ctx) {
    const deck = [];
    for (let c = 0; c < COLOURS.length; c++) for (let k = 0; k < PER_COLOUR; k++) deck.push(c);
    ctx.shuffle(deck);
    const hands = [];
    for (let p = 0; p < ctx.seats; p++) {
      const hand = [];
      for (let k = 0; k < START_HAND; k++) hand.push({ c: deck.pop(), seen: false });
      hands.push(hand);
    }
    const state = {
      deck, market: [], hands,
      scores: Array(ctx.seats).fill(0),
      bonus: Array(COLOURS.length).fill(null), // seat that won each festival bonus
      turn: ctx.randomInt(ctx.seats),           // a random first player
      lastCall: null,                           // final turns left once the supply is empty
      over: false,
    };
    refillMarket(state);
    return state;
  },

  toMove(state) {
    return state.over ? [] : [state.turn];
  },

  legal(state, seat) {
    if (state.over || seat !== state.turn) return [];
    const hand = state.hands[seat];
    const sorted = sortedHand(hand);
    const moves = [];

    // Selling is always allowed when you hold the colour.
    for (let c = 0; c < COLOURS.length; c++) {
      const n = countOf(hand, c);
      if (n === 0) continue;
      const bonus = state.bonus[c] === null && n >= FESTIVAL_MIN ? FESTIVAL_BONUS : 0;
      moves.push({
        type: "sell",
        label: `Sell ${plural(n, COLOURS[c].name + " lantern")} (+${setValue(n) + bonus})`,
        args: { colour: c },
        ui: { zone: `hand-${seat}`, index: sorted.findIndex((card) => card.c === c) },
      });
    }

    if (state.lastCall !== null) {
      moves.push({ type: "pass", label: "Close your stall" });
      return moves;
    }
    if (hand.length < HAND_LIMIT) {
      state.market.forEach((c, i) => {
        moves.push({ type: "take", label: `Take the ${COLOURS[c].name} lantern`, args: { index: i }, ui: { zone: "market", index: i } });
      });
      if (state.deck.length > 0) moves.push({ type: "draw", label: "Draw blind from the deck" });
    }
    return moves;
  },

  apply(state, seat, move, ctx) {
    const hand = state.hands[seat];
    const events = [];

    if (move.type === "take") {
      const c = state.market.splice(move.args.index, 1)[0];
      hand.push({ c, seen: true });
      refillMarket(state);
      events.push({ type: "take", seat, text: `{s:${seat}} takes a ${COLOURS[c].name} lantern from the market` });
    } else if (move.type === "draw") {
      const c = state.deck.pop();
      hand.push({ c, seen: false });
      // Public event without the colour; private event with it.
      events.push({ type: "draw", seat, text: `{s:${seat}} draws blind from the deck` });
      events.push({ type: "drew", seat, text: `{s:${seat}} drew a ${COLOURS[c].name} lantern`, only: [seat] });
    } else if (move.type === "sell") {
      const c = move.args.colour;
      const n = countOf(hand, c);
      state.hands[seat] = hand.filter((card) => card.c !== c);
      let points = setValue(n);
      let text = `{s:${seat}} sells ${plural(n, COLOURS[c].name + " lantern")} for ${points}`;
      if (state.bonus[c] === null && n >= FESTIVAL_MIN) {
        state.bonus[c] = seat;
        points += FESTIVAL_BONUS;
        text += ` and wins the ${COLOURS[c].name} festival bonus (+${FESTIVAL_BONUS})`;
      }
      state.scores[seat] += points;
      events.push({ type: "sell", seat, text, data: { colour: c, count: n, points } });
    } else {
      events.push({ type: "pass", seat, text: `{s:${seat}} closes their stall` });
    }

    // End of turn: the last-call countdown, then the next player.
    if (state.lastCall !== null) {
      state.lastCall -= 1;
    } else if (supplyEmpty(state)) {
      state.lastCall = state.hands.length - 1;
      events.push({ type: "last_call", seat, text: "The market is empty: last call! Everyone else may sell once more." });
    }
    if (state.lastCall === 0) {
      state.over = true;
      state.hands.forEach((h, p) => {
        if (h.length > 0) {
          state.scores[p] -= h.length;
          events.push({ type: "penalty", seat: p, text: `{s:${p}} is left with ${plural(h.length, "unsold lantern")} (−${h.length})` });
        }
      });
    } else {
      state.turn = nextSeat(state, seat);
    }
    return { state, events };
  },

  // Each seat sees its own hand, the market, everyone's public cards and the
  // number of hidden cards in each other hand. Never the deck order or the
  // colour of another player's blind draws: a hidden card has no face at all.
  view(state, seat) {
    const zones = [{
      id: "market", label: "Market", layout: "row",
      cards: state.market.map((c) => ({ face: COLOURS[c].name, color: COLOURS[c].hex })),
    }];
    state.hands.forEach((hand, p) => {
      let cards;
      if (p === seat) {
        cards = sortedHand(hand).map((card) => ({ face: COLOURS[card.c].name, color: COLOURS[card.c].hex }));
      } else {
        // Public cards first, then backs: the order must not hint at colours.
        const open = sortedHand(hand.filter((card) => card.seen));
        cards = open.map((card) => ({ face: COLOURS[card.c].name, color: COLOURS[card.c].hex }));
        for (let k = open.length; k < hand.length; k++) cards.push({ hidden: true });
      }
      zones.push({ id: `hand-${p}`, label: p === seat ? "Your lanterns" : "Lanterns", owner: p, layout: "fan", cards });
    });

    let message;
    if (state.over) {
      const best = Math.max(...state.scores);
      const winners = state.scores.map((s, p) => (s === best ? `{s:${p}}` : null)).filter((x) => x !== null);
      message = winners.length === 1 ? `${winners[0]} wins with ${best} points` : `${winners.join(" and ")} tie with ${best} points`;
    } else if (state.lastCall !== null) {
      message = `Last call: {s:${state.turn}} may sell once more`;
    } else {
      message = `{s:${state.turn}} to take, draw or sell`;
    }

    return {
      title: "Lantern Market",
      zones,
      players: state.hands.map((hand, p) => ({
        seat: p, score: state.scores[p], info: plural(hand.length, "lantern"), color: `p${p}`,
      })),
      counters: [
        { label: "Deck", value: state.deck.length },
        { label: "Festival bonuses left", value: state.bonus.filter((b) => b === null).length },
      ],
      message,
    };
  },

  outcome(state) {
    if (!state.over) return null;
    const rank = state.scores.map((s) => 1 + state.scores.filter((t) => t > s).length);
    const best = Math.max(...state.scores);
    const winners = state.scores.map((s, p) => (s === best ? `{s:${p}}` : null)).filter((x) => x !== null);
    const summary = winners.length === 1
      ? `${winners[0]} wins the market with ${best} points`
      : `${winners.join(" and ")} share the market with ${best} points`;
    return { rank, score: state.scores.slice(), summary };
  },

  // Called with the real state and a seat: return a state in which
  // everything that seat cannot know is reshuffled. Here that is the deck
  // order and the colours of other players' blind draws; their market takes
  // (seen) stay put. Hand sizes never change. The AI players search many such
  // samples instead of peeking at the real hidden cards.
  determinize(state, seat, ctx) {
    const pool = state.deck.slice();
    state.hands.forEach((hand, p) => {
      if (p === seat) return;
      for (const card of hand) if (!card.seen) pool.push(card.c);
    });
    ctx.shuffle(pool);
    state.hands = state.hands.map((hand, p) =>
      p === seat ? hand : hand.map((card) => (card.seen ? card : { c: pool.pop(), seen: false })));
    state.deck = pool;
    return state;
  },

  // On timeout: sell the biggest set when forced or at last call, otherwise
  // draw blind (never a terrible move).
  defaultMove(state, seat) {
    const hand = state.hands[seat];
    if (state.lastCall !== null || hand.length >= HAND_LIMIT) {
      let best = -1, bestN = 0;
      for (let c = 0; c < COLOURS.length; c++) {
        const n = countOf(hand, c);
        if (n > bestN) { best = c; bestN = n; }
      }
      return best >= 0 ? { type: "sell", args: { colour: best } } : { type: "pass" };
    }
    if (state.deck.length > 0) return { type: "draw" };
    return { type: "take", args: { index: 0 } };
  },
};
