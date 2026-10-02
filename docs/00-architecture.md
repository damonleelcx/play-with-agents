# Play with Agents: architecture and contracts

Build team: this file is the contract between the backend, the frontend and
the game runtimes. If you change a shape here, change it everywhere.

## Product

**Play with Agents** is a game table that is always full. People sit down with
friends, with AI agents, or with both. They play Texas Hold'em out of the box,
and they can describe a board game of their own in conversation. The agents
then design it, build it, playtest it and set it on the table.

- Host agent: **Aoi (葵)**, an AI agent player. Japanese, smart, friendly,
  competitive, curious. She hosts, explains rules, plays, banters, coaches when
  asked and runs the game studio. Her soul is described in [02-soul.md](02-soul.md).
- The AI player roster (all play poker and every custom game):
  | id | name | style (poker) | persona |
  |---|---|---|---|
  | `aoi` | Aoi 葵 | balanced, adaptive | the host; warm, teasing, competitive |
  | `ren` | Ren 蓮 | tight-aggressive | calm strategist, few words, dry humour |
  | `mika` | Mika 美香 | loose-aggressive, bluffs | fearless showoff, loud, loves all-ins |
  | `bram` | Captain Bram | loose-passive (calls a lot) | old sailor, tells stories, never folds a good yarn |
  | `nova` | Nova | balanced, math-driven | cheerful robot, quotes odds, terrible jokes |
  | `lin` | Lin 琳 | tight-passive, rarely bluffs | shy prodigy, polite, quietly deadly |
- Chips are play money. There is no purchase, no cash-out and no real-money
  wagering anywhere. The landing page, the rules and the terms say so.
- Bilingual: English and 中文 across the site, emails and Aoi's replies.
- Deployed at `https://play.heros-agent.space`, namespace `play` on the
  heros-prod k3s node. Env prefix `PLAY_`, binary `play` (`serve|web|worker|migrate|mailcheck`).

## Agent system (the principles, mapped)

```
User goal (chat with Aoi)
  ↓ Router (fast model): intent = chat | play | build_game | revise_game | rules | control | preference
  ↓
Mission Planner (engine.Planner) → task DAG (skills = playbooks)
  ↓
Durable queue (Postgres, FOR UPDATE SKIP LOCKED, leases + fencing epoch)
  ↓
Specialised workers
  → Designer   writes the rules document           (LLM, tool save_rules)
  → Engineer   writes the game module (JavaScript) (LLM, tools save_module, check_module)
  → Playtester simulates hundreds of games          (deterministic tool playtest; no LLM)
  → Critic     reviews rules vs module vs report    (LLM, independent of the Engineer)
  → Coordinator replans on failed checks/critique   (engine.Planner.Replan)
  ↓
Verification (check_module + playtest gates; critic verdict) → checkpoint → publish approval (G1, the user)
```

Two queues, both durable in Postgres:

1. **Mission tasks** (`tasks`, inherited from the engine): long-running, LLM
   work: designing and building games. Goals, DAGs, checkpoints, approvals,
   budgets, replanning, timeline.
2. **Table jobs** (`table_jobs`): short, latency-sensitive work at the tables:
   `agent_move`, `turn_timeout`, `agent_chat`. Same claiming discipline (skip
   locked + lease + epoch). Idempotency key `(table_id, kind, seat, state_version)`
   is unique, so a re-enqueue or a duplicate worker is a no-op. A job whose
   `state_version` no longer matches the table is stale and completes without
   effect.

Table state transitions use optimistic concurrency: `UPDATE tables SET state=$new,
version=version+1 WHERE id=$id AND version=$expected`. Every accepted move is a
`table_moves` row (unique `(table_id, client_move_id)` for human retries,
unique `(table_id, seq)`), so a table can be rebuilt by replay from its setup seed.
After each commit the writer runs `pg_notify('play_table', table_id)`; every web
pod LISTENs and fans out to that table's SSE subscribers.

## Game contract

`internal/games/game.go` (Go). Built-ins register in `games.Register`.
Custom games are JavaScript modules run by `internal/games/script` (goja,
sandboxed: no I/O, no clock, seeded randomness only, a time budget and a memory
ceiling per call).

### Script module contract

The module source defines a global `game`:

```js
const game = {
  meta: { name: "Connect Four", summary: "Drop discs, line up four.",
          minSeats: 2, maxSeats: 2, hiddenInfo: false, turnSeconds: 30,
          options: {} },
  setup(ctx) { return { /* any JSON */ } },          // ctx = { seats, options, random() }
  toMove(state) { return [state.turn] },             // [] when over
  legal(state, seat) { return [{ type, label, args, ui }] },
  apply(state, seat, move, ctx) { return newState }, // or { state, events:[{type,seat,text}] }; throw to reject
  view(state, seat) { return { /* board view, below */ } },
  outcome(state) { return null /* or { rank:[...], score:[...], summary } */ },
  defaultMove(state, seat) {},                       // optional; first legal otherwise
  heuristic(state, seat) {},                         // optional, -1..1, helps agents
  determinize(state, seat, ctx) {},                  // optional, hidden-info games: resample what seat cannot see
}
```

The runtime owns the random stream (stored next to the module's state), so
`ctx.random()` is deterministic across replays. `Math.random` and `Date` are
removed.

### View kinds

`holdem` — see the Hold'em section. `board` — the generic renderer:

```jsonc
{
  "title": "Connect Four",
  "board": {                         // optional
    "rows": 6, "cols": 7,
    "style": "grid",                 // grid | checker | go | plain
    "cells": [[ /* row-major; null or Cell */ ]]
  },
  // Cell = { "piece": { "shape": "disc|square|ring|king|text", "color": "p0..p7|#hex|css",
  //                      "glyph": "♛", "label": "K" },
  //          "mark": "#hex|css",        highlight
  //          "text": "3" }
  "zones": [                         // optional: hands, piles, decks
    { "id": "hand-0", "label": "Your hand", "owner": 0, "layout": "row|fan|stack",
      "cards": [ { "face": "7♥", "color": "#c33", "hidden": false } ] }
  ],
  "players": [ { "seat": 0, "score": 3, "info": "Red", "color": "p0" } ],
  "counters": [ { "label": "Round", "value": "2 / 5" } ],
  "message": "Red to move"
}
```

Move UI hints (`MoveSpec.ui`): `{cell:[r,c]}` (click a cell), `{from:[r,c],to:[r,c]}`
(select piece, then target), `{zone:"hand-0",index:2}` (click a card). Moves
without hints render as buttons. A `range` renders as a slider + input.

Player colours `p0..p7`: `#4f8cff #ff5d73 #ffc04d #3ddc97 #b07cff #ff8f40 #3fd2ff #e6e6e6`.

### Event and status text

Games never know player names. `Event.Text` and `View.Status` refer to seats
as `{s:N}` (e.g. `"{s:2} raises to 120"`); the rooms service substitutes display
names before anything reaches a client or an agent prompt.

## Texas Hold'em (`internal/games/holdem`, id `holdem`)

No-limit hold'em, 2 to 9 seats, play-money chips. Options (defaults):
`starting_stack` 1000, `small_blind` 10, `big_blind` 20,
`blinds_double_every` 10 (hands, 0 = never), `max_hands` 0 (0 = until one player has every chip).

Moves: `fold`, `check`, `call`, `raise {to}` (total this-street bet; a bet when
no one has bet yet; label "Bet" or "Raise to"; `range` min = min-raise-to,
max = all-in), `allin`. Default move on timeout: check if legal, else fold.
A completed hand deals the next in the same `Apply`; the finished hand is in
`last_hand` so the client can show the result.

Showdown order (a house rule; there is no auto-muck setting): all-in players'
hands are always shown (tabled before the run-out). Then the river's last
aggressor shows first, or, if nobody bet the river, the first player left of
the button, and the rest follow clockwise. Each of them shows only if the hand
beats or ties the best hand shown so far; otherwise it is mucked: a `muck`
event (`"{s:N} mucks"`), no cards, not in `last_hand.shown`. A hand that wins
any pot is always shown. Reveal events come before the `win` events.

Cards: `"As" "Td" "9c" "2h"` (rank `23456789TJQKA`, suit `shdc`).

View `data`:

```jsonc
{
  "hand_no": 7, "hands_left": null,
  "street": "preflop|flop|turn|river|showdown|over",
  "board": ["As","Kd","7c"],
  "pots": [ { "amount": 340, "eligible": [0,2,3] } ], "pot_total": 340,
  "button": 2, "sb_seat": 3, "bb_seat": 0,
  "small_blind": 10, "big_blind": 20,
  "current_bet": 60, "min_raise_to": 100,
  "to_act": 1,                                     // -1 when no one
  "players": [ {
     "seat": 0, "stack": 940, "bet": 20, "total_bet": 60,
     "status": "active|folded|allin|out",
     "cards": ["Ah","Kh"],                          // null unless it is the viewer's or shown at showdown
     "last_action": "check|call|bet|raise|fold|allin|sb|bb|",
     "hand_name": "Two Pair"                       // the viewer's own current best hand, or at showdown
  } ],
  "last_hand": {                                   // null before the first hand ends
     "hand_no": 6, "board": [...],
     "winners": [ { "seat": 2, "amount": 480, "hand_name": "Flush, Ace high", "cards": ["Ac","Jc"] } ],
     "shown": [ { "seat": 1, "cards": ["Qd","Qs"], "hand_name": "Pair of Queens" } ]
  }
}
```

## HTTP API

JSON, cookie session (`play_session`), all under `/api`. Auth, settings,
sessions, memories, conversations, goals, approvals and usage keep the shapes
inherited from the engine (see `internal/httpapi`), except:

- `POST /api/auth/verify { token }` → `{ verified: true, signed_in: bool, user? }`. It
  never creates a session (the link proves the mailbox, not who registered the
  account). `user` is returned only to a browser already signed in to that account;
  anyone else is told "Email confirmed — sign in to continue".
- Errors from sign-up, password change/reset and session revoke are user-facing
  messages only; anything else is logged and answered with a generic message.

New:

### Games
- `GET /api/games` → `{ builtin: GameCard[], mine: GameCard[], community: GameCard[] }`
- `GET /api/games/{id}` → `GameCard & { rules_md, versions: [{version, created_at, report}] , goal_id? }`
- `PATCH /api/games/{id}` `{ name?, summary?, visibility? }` (owner)
- `DELETE /api/games/{id}` (owner; drafts only)

`GameCard = { id, kind: "builtin"|"script", name, summary, min_seats, max_seats, hidden_info,
status: "building"|"draft"|"published", visibility: "private"|"unlisted"|"public",
owner_name?, version, plays, cover? }` (built-in Hold'em has id `holdem`).

### Agents
- `GET /api/agents` → `[{ id, name, name_zh, title, title_zh, bio, bio_zh, avatar,
  style: { tightness, aggression, bluff, talk } }]` (0..1 each)

### Tables
- `POST /api/tables` `{ game_id, name?, options?, turn_seconds?, seats: [{ kind: "me"|"agent"|"open", agent_id? }] }` → `TableView`
- `GET /api/tables?scope=mine|open` → `TableSummary[]` (`{ id, name, code, game_name, status, seats_taken, seats_total, updated_at }`)
- `GET /api/tables/{id}` → `TableView`
- `POST /api/tables/join` `{ code }` → `TableView` (takes the first open seat, or spectates if full)
- `PUT /api/tables/{id}/seats/{seat}` `{ kind: "agent", agent_id } | { kind: "open" }` (host, lobby only)
- `POST /api/tables/{id}/start` (host) → `TableView`
- `POST /api/tables/{id}/moves` `{ client_move_id, version, move: {type, args} }` → `TableView`;
  `409 { error, table }` when `version` is stale; `422 { error }` when illegal.
  A retry with the same `client_move_id` and the same move returns the current view;
  the same id with a different move is `409 { error }`. Only people who may watch the
  table get either answer (403 otherwise).
- `POST /api/tables/{id}/chat` `{ text, client_msg_id }` → `{ ok }`
- `POST /api/tables/{id}/leave` → `{ ok }` (an agent takes over a seat left mid-game)
- `POST /api/tables/{id}/back` → `TableView` ("I'm back": clears my seat's `away` and resumes a paused table)
- `POST /api/tables/{id}/rematch` (host) → `TableView` of the new table
- `GET /api/tables/{id}/stream` → SSE: `event: table` `{ version }` (refetch), `event: chat` `ChatLine`.
  Access is re-checked while the stream runs (before an event once the last check is 5s old,
  and on every 20s keepalive); someone who can no longer watch (left as a spectator, signed
  out, account deleted) gets `event: closed` `{ reason: "access" }` and the stream ends.

"Host" actions (start, seats, rematch, resuming after a fault) belong to `host_id`.
`host_id` is `""` when the table has no host (the host deleted their account and
nobody else was seated to take over); then any person seated has the host's rights,
and `is_host` says so.

```jsonc
TableView = {
  "id", "name", "code",                         // code = 6-char invite code
  "game": { "id", "name", "kind" },
  "status": "lobby|playing|finished|abandoned",
  "host_id", "is_host": true, "my_seat": 0,     // -1 = spectator
  "version": 42,
  "seats": [ { "seat": 0, "kind": "human|agent|open", "name", "avatar", "agent_id"?, "is_me": true, "away"?: true } ],
  "paused": false,                              // nobody attending: status stays playing, nothing runs
  "paused_reason": "idle|fault",                // only while paused (omitted otherwise)
  "to_move": [1], "deadline": "2026-10-02T12:00:30Z" | null, "turn_seconds": 30,
  "legal": [ MoveSpec ],                        // only for my seat when it is my turn
  "view": { "kind": "holdem|board", "data": {...}, "status": "..." },
  "log":  [ { "seq", "type", "seat", "text", "at" } ],      // last 60 visible to me
  "chat": [ ChatLine ],                                       // last 60
  "outcome": Outcome | null
}
ChatLine = { "id", "seat", "name", "avatar", "text", "at", "agent": true }
```

**Away and paused.** After 2 consecutive clock run-outs a person's seat is
`away`: their turns resolve with the default move after a ~1.5s grace (the
`deadline` shows it) instead of the full clock; agents keep their pace. Their
own move or `POST …/back` clears it. When every person at a playing table has
been away for 15 minutes, or no person has done anything (move, chat, back)
for 15 minutes, the sweep pauses the table: `paused: true`, `deadline: null`,
no agent moves, clocks or table talk run. A person's move or `/back` resumes
it. Unattended tables are still abandoned after 2 hours.

**Faults.** A move or clock job that fails all its attempts (errors, or workers
dying with the lease) is followed by the seat's default move. If even that cannot
be played, the table pauses with `paused_reason: "fault"` and a `fault` log event
("This game hit a problem and was paused"). Moves are refused while it is paused
this way; the host (see above) resumes it with `POST …/back`, which retries. A
second fault abandons the table (`fault_closed` event).

**Deleting an account** (`POST /api/account/delete`) first hands everything over
(`rooms.ReleaseUser`, migration 0013): each seat the person holds mid-game is taken
over by an agent as with leave (`takeover` event; the seat keeps its chips), a
lobby seat opens; host rights pass to the next person seated (`host` event) or to
no one; a lobby or game nobody else is in or watching is abandoned (`closed`
event). Their published non-private games stay, credited to "a former player"
(`owner_name`), and so do all tables of them. Drafts and private games are deleted
with their versions, unless someone else has a table of them: then the live ones
are abandoned (`closed` event) and the game is kept hidden and ownerless so those
records still render. Finished tables keep their seating as a record.

**Table talk cadence.** Unprompted agent lines scale with the square of the
persona's `talk`, so Ren and Lin are rare; pots under 10 big blinds almost
never prompt a line. Each agent has a cooldown after its own line (45s for the
chattiest, ~2.5 min for the quietest), a table gets at most 8 unprompted agent
lines per 10 minutes (replies to @mentions: 20), and a line too close to one
of the agent's own last 5 lines is dropped (silence, not a canned line).

### Studio (building games)
Building a game is a mission (goal kind `build_game`). It can be started from
chat ("let's make a game where…") or `POST /api/studio/build { prompt, base_game_id? }`
→ `{ goal_id, game_id }`. Progress uses the goal endpoints (`/api/goals/{id}`,
`/timeline`, `/approvals`). The final step asks the owner to approve publishing;
a draft can be played by its owner at any time (`POST /api/tables` with its id).

### Chat cards
Aoi's message `meta.cards`: `[{ kind: "table", table_id } | { kind: "mission", goal_id } | { kind: "game", game_id }]`.

## Settings (user_preferences keys)

| group | key | values |
|---|---|---|
| Profile | `display_name`, `language` | text; `en`/`zh` |
| Aoi | `aoi_tone` | `playful` `calm` `competitive` |
| | `aoi_talk` | `chatty` `normal` `quiet` |
| | `aoi_coaching` | bool: tips during your own hands |
| | `call_me` | nickname |
| | `memory_enabled` | bool |
| Table | `turn_seconds` | `15` `30` `60` `0` (no clock vs agents) |
| | `agent_speed` | `fast` `natural` `slow` |
| | `table_talk` | `all` `quiet` `off` |
| | `four_color_deck`, `show_hand_strength`, `sound` | bool (no muck setting: showdowns follow the house order, see Hold'em) |
| | `motion` | `full` `reduced` |
| | `card_back` | `aoi` `classic` `midnight` |
| | `felt` | `navy` `emerald` `crimson` |
| Agents | `agent_difficulty` | `casual` `regular` `shark` |
| | `fill_empty_seats` | bool |
| | `favorite_agents` | list of agent ids |
| Studio | `studio_visibility` | `private` `unlisted` `public` |
| | `playtest_games` | `50` `200` `500` |
| Appearance | `theme` | `dark` `light` `system` |
| | `font_size` | `small` `medium` `large` |
| Limits | `goal_max_cost_usd`, `goal_max_days` | per studio build ceilings (1–200 USD, 1–180 days) |
| Notifications | `email_table_invites`, `email_your_turn`, `email_build_done` | bool |

Plus the inherited Security (password, sessions), Privacy & memory (export,
memories, delete account) and Usage & limits pages.

## Assets

`web/public/play/aoi/`: `aoi-full.webp` (full body, transparent or dark
background), `aoi-portrait.webp`, `aoi-card.webp` (holo card art),
`aoi-face-{neutral,smile,wink,surprised,angry,sad}.webp`,
`aoi-outfit-{default,casual,combat,summer}.webp`, `aoi-action.webp`,
`aoi-sheet.webp`. `web/public/play/agents/{aoi,ren,mika,bram,nova,lin}.webp` (square avatars).

## Aoi's voice

Aoi speaks in the same Fish Audio voice as jobs.heros-agent.space (阿桥) and
forge.heros-agent.space: published model `df5c6c19dca944918dcbd6f1368fd02f`,
backbone `s2.1-pro-free` (Fish's free backbone; its terms allow the vendor to
train on requests, the same deliberate choice FORGE made. Privacy policy says
so; switch PLAY_TTS_MODEL to `s2.1-pro` if that must change).

- Config: `PLAY_TTS_API_KEY` (secret; production reads `opportunity-bridge/model`
  property `OBA_TTS_API_KEY`, the estate's shared Fish key), `PLAY_TTS_VOICE_ID`,
  `PLAY_TTS_MODEL`. No key → no voice endpoint; the client hides voice controls.
- `GET /api/speech` → `{ enabled: bool }`.
- `POST /api/speech` → `audio/mpeg`. It speaks only lines the server already holds
  as Aoi's, never free text. The body is exactly one of:
  - `{ message_id }`: one of Aoi's replies (`role: assistant`) in a conversation the caller owns;
  - `{ table_id, chat_id }`: one of Aoi's (`agent_id: aoi`) table-talk lines at a table the caller may watch;
  - `{ sample: "en" | "zh" }`: the fixed "Hear Aoi" line of the settings page (the strings live on the server).

  Signed in + verified. 400 for anything else (including `{ text }`), 404 for a line
  that is not the caller's or not Aoi's, 403 for a table they cannot watch. Text is
  cleaned of markdown/emoji and capped at 600 chars; rate limit 20/min per user; a
  daily allowance per account of `PLAY_TTS_DAILY_CHARS` characters (default 20000,
  UTC days, counted in Postgres `tts_usage`, 429 when used up). Identical lines are
  cached in memory (LRU, ~200 entries): replays cost nothing and are not counted.
  503 when not configured.
- Web client (`web/src/lib/voice.ts`): `speakMessage(id)`, `speakTableLine(tableId, chatId)`,
  `speakSample(lang)`.
- Only Aoi has this voice. Other agents' table talk is text.
- Settings (Aoi group): `aoi_voice` bool (default true: a speaker button on her
  messages), `voice_autoplay` bool (default false: read her new chat replies
  aloud), `table_voice` bool (default false: read Aoi's table-talk lines aloud
  at tables), `voice_volume` 0..100 (default 80).
