# 01 · Intents, tools and skills

This document lists everything Aoi can be asked to do (**intents**), the
capabilities she acts through in chat (**chat tools**), every action an agent
can take through the mission engine (**mission tools**), and the multi-step
playbooks (**skills**). The router, the handlers, the planner, the worker, the
approval gates and the UI are built from it. A capability that is missing here
does not exist in the product.

---

## 0. The boundary everything else is built around

Agents on this platform play games, explain them and build them. They never
move money (there is none: chips are play money), never reach outside the
platform, and never act for a player without asking when the action is
visible to other people or cannot be undone. That is enforced by **gates**
(§7), by **confirmation turns** (§3) and by the game contract (agents and the
coach reason from what their seat can see), not by prompt wording.

---

## 1. Intents

The router (`internal/agent/router.go`, fast model, JSON, temperature 0)
classifies every chat turn into exactly one of **33 intents** in 10 areas and
fills the intent's slots. Its prompt carries, besides the intent menu, the
ROSTER, the player's CATALOG (with status/visibility of their own games), OPEN
MISSIONS, YOUR TABLES (open tables with code and pause state) and the last
messages. With `confidence < 0.5` and a clarifying question, Aoi asks instead
of acting on any intent that changes something (an unsure router never creates
a table, deletes a draft or starts a build); read-only intents are answered.

The handler (`internal/agent/handlers_*.go`) calls the capability, then writes
a **brief** for the reply ("FOR THIS REPLY: …" in the system prompt): what
happened, the facts to use, what to ask. Aoi's reply is streamed in the
player's language; the brief never reaches the player verbatim.

Conventions in the tables below:

- **Gate** — `G0` automatic; `G0✓` automatic but behind a **confirmation
  turn** (§3); `G1` the owner's approval on the engine's approval record (and
  a confirmation turn when given in chat); `G3` never done from chat.
- **Card** is `meta.cards` (`table` / `mission` / `game`), rendered under the
  reply by the app. **Mood** is `meta.mood` (Aoi's face).
- Every capability may be nil (not wired on a server): Aoi says that part is
  not open yet (mood `sad`). A capability error wrapping `agent.ErrRejected`
  is about the request and its reason reaches the reply ("Connect Four seats
  exactly 2 players"); any other error is logged and the player hears a short
  apology with "nothing changed".

### 1.1 Play

| Intent | Examples (en / 中文) | Slots | Chat tool | Gate | Card · mood | Failure replies |
|---|---|---|---|---|---|---|
| `play` | "deal me into hold'em with Mika and Ren", "game night with Mika and two friends", "Star Race with shark bots and a 60s clock" / "开一桌德州，叫上美香和蓮", "留两个位置给朋友，盲注10/20" | `game_id` (default holdem), `agent_ids`, `friends`, `seats`, `options` (game options; table settings `difficulty`, `turn_seconds`, `agent_speed`, `table_talk` are split off) | `Tables.CreateTable` (→ `rooms.Create`, `rooms.ApplyLobbySettings`, `rooms.Start` when no seat is open) | G0 | `table` · wink | unknown game → names playable ones, offers to build it; still building → game card; more seats than the game has → nothing created, asks who sits out; rejected options/settings → reason |
| `invite` | "invite my friends", "what's the table code?" / "把邀请链接发给我", "这桌的房间码是多少？" | `table_id` | `Tables.MyTables` (+ `Agent.PublicOrigin` for `/join/CODE`) | G0 | `table` · smile | no open table → offers to set one up; playing/full → friends join as spectators |
| `join_table` | "join K7M2QX", "my friend gave me HJK234" / "加入房间 QW3ERT" | `code` (also read from the message) | `Tables.JoinTable` (→ `rooms.Join`) | G0 | `table` · wink | no/invalid code → asks for it; no live table with the code → says so; full or started → joined as spectator |
| `list_tables` | "show my tables", "anything still going?" / "我现在有哪些牌桌？" | `action` open\|all | `Tables.MyTables` (→ `rooms.PlayerTables`) | G0 | up to 3 `table` · neutral | none → offers a table |
| `resume_table` | "resume my last game" / "继续上一局", "带我回到刚才那桌" | `table_id` | `Tables.BackToTable` (→ `rooms.Back`) | G0 | `table` · smile | none unfinished → offers new table or rematch; fault-paused → only the host resumes |
| `rematch` | "rematch!", "run it back" / "再来一局！" | `table_id` | `Tables.MyTables(finished)`, `Tables.Rematch` | G0 | `table` · wink | no finished game → offers a new table; game not over → says so; not host → reason |
| `leave_table` | "I'm leaving the table" / "我不玩了，退出牌桌" | `table_id` | `Tables.LeaveTable` (→ `rooms.Leave`) after yes | **G0✓** | — · neutral | not at a table → says so; confirmation explains an agent takes over mid-game, or the lobby closes for everyone (host) |
| `im_back` | "I'm back" / "我回来了" | `table_id` | `Tables.BackToTable` | G0 | `table` · smile | no table waiting → welcome back + offer to deal |

### 1.2 Help at the table

| Intent | Examples (en / 中文) | Slots | Chat tool | Gate | Card · mood | Failure replies |
|---|---|---|---|---|---|---|
| `coach` | "what should I do?", "should I call this?" / "这手牌我该跟还是弃？" | `table_id` | `Coach.Advice` (→ `rooms.CoachFor`: the caller's seat view, legal moves, `holdem.Coach` for hold'em) | G0 | `table` · neutral | not seated at a playing table → general tips + offer to deal; spectator → "no hand of yours" |
| `rules` | "how do side pots work?", "what beats a flush?" / "同花和顺子哪个大？" | `game_id` | `Catalog.Rules` (rules text is authoritative) | G0 | `game` (if a game) · neutral | rules unavailable → answers how it's usually played |

**Coaching never sees other seats.** `rooms.CoachFor` refuses anyone not
seated (a spectator, the host of an all-agent table) and builds the report
from `Game.View(state, callerSeat)` and `holdem.Coach(state, callerSeat)`,
which itself works only from that view. The brief tells the reply never to
claim or guess others' hidden cards. Tested by
`rooms.TestCoachForUsesOnlyTheCallersSeat` (every other seat's hole cards are
absent from the report, own cards present) and
`agent.TestCoachAsksForTheCallersOwnSeatOnly`.

### 1.3 Agents

| Intent | Examples (en / 中文) | Slots | Chat tool | Gate | Card · mood | Failure replies |
|---|---|---|---|---|---|---|
| `agents_info` | "who are the agents?", "tell me about Mika" / "美香是什么风格的玩家？" | `agent_ids` (empty = all) | `internal/agents` roster (bios, style numbers) | G0 | — · smile | — |
| `favorite_agents` | "make Nova a favourite", "remove Bram" / "把琳加入我的收藏" | `action` add\|remove\|set\|show, `agent_ids` | preference `favorite_agents` (validated) | G0 | — · smile | no agents named → asks which |

"Play with X" is `play`; "make the agents harder" is `preference agent_difficulty`
(or `difficulty` for one table inside `play`).

### 1.4 Games

| Intent | Examples (en / 中文) | Slots | Chat tool | Gate | Card · mood | Failure replies |
|---|---|---|---|---|---|---|
| `recommend_game` | "what should four of us play?" / "推荐一个适合三个人玩的游戏" | `game_id` (router's pick), `seats` | catalog (`Catalog.Games`) | G0 | `game` (if the pick seats them) · smile | pick that doesn't seat them → no card, model chooses from the list |
| `list_games` | "what games can I play?", "show my games" / "这里都有什么游戏？", "我做过哪些游戏？" | `action` all\|mine | `Catalog.Games` | G0 | up to 4 `game` (mine) · smile | no own games → invites them to build one |
| `game_info` | "tell me about Star Race" / "介绍一下Star Race这个游戏" | `game_id` | `Catalog.Game`, `Catalog.Rules` | G0 | `game` · smile | not found → names playable ones |
| `game_visibility` | "publish Moon Tiles to the shelf", "make Dragon Chess private", "share by link only" / "把Moon Tiles设为私密" | `game_id`, `visibility` public\|unlisted\|private | `Catalog.SetVisibility` (→ `rooms.PatchGame`) | private/unlisted G0; **public G0✓** | `game` · smile | not theirs → refused before asking; unclear → asks which; draft → applies once published; unlisted → link `/app/games/ID` |
| `delete_game` | "delete my Dragon Chess draft" / "删掉Dragon Chess那个草稿" | `game_id` | `Catalog.DeleteDraft` (→ `rooms.DeleteGame`) after yes | **G0✓** | — · neutral | building → offer to cancel the build first; published → never deleted, offer private |
| `rename_game` | "rename Dragon Chess to Wyrm Wars" / "把Dragon Chess改名为飞龙棋" | `game_id`, `name` | `Catalog.Rename` (→ `rooms.PatchGame`) | G0 | `game` · smile | no name → asks; > 60 chars → asks for shorter |

Owner-only intents resolve the game among the player's own; with no
`game_id` and exactly one own game, that one; otherwise Aoi asks which.

### 1.5 Studio

| Intent | Examples (en / 中文) | Slots | Chat tool | Gate | Card · mood | Failure replies |
|---|---|---|---|---|---|---|
| `build_game` | "let's make a game where…" / "我们来做一个游戏吧：…" | `prompt`, `base_game_id` | `Studio.StartBuild` (goal skill `build_game`) | G0 (publishing later is G1) | `mission` · smile | studio not open → loves the idea, offers to talk rules |
| `revise_game` | "change the build to 3 players", "make Moon Tiles easier, then republish" / "把正在做的游戏改成三个人玩" | `goal_id` or `game_id`, `prompt` | running build: `Store.EnqueuePlan(change)`; finished own game: `Studio.StartBuild(base)` | G0 | `mission` · smile | not theirs / unclear → asks which; already building → send change to that build |
| `control` | "pause the build", "resume", "cancel the Dragon Chess build", "how's it going?", "why did it fail?" / "先暂停一下游戏制作", "我的游戏做到哪一步了？", "游戏制作为什么卡住了？" | `action` pause\|resume\|cancel\|status\|why, `goal_id` | `Store.GoalControl` (same as the mission page buttons), `Store.Tasks` | pause/resume G0; **cancel G0✓** | `mission` · neutral/smile/sad | no build → offers one; several → asks which; nothing to pause/resume → says so; `why` → recorded attention reason + failed tasks' errors |
| `approve_publish` | "approve publishing Dragon Chess", "not yet, keep it a draft" / "批准发布", "先别发布，留作草稿" | `action` approve\|decline, `goal_id` | `Store.PendingApprovals` (own G1 approvals only), `Store.DecideAsOwner` → `Store.Decide` | **G1**: approve after yes; decline G0 | `mission` · smile/neutral | nothing waiting → says so; several → asks which; decided meanwhile → says so |

### 1.6 Account and settings

| Intent | Examples (en / 中文) | Slots | Chat tool | Gate | Card · mood | Failure replies |
|---|---|---|---|---|---|---|
| `preference` | "call me Captain", "be quieter", "sound off and light theme", "shark difficulty", "60-second clock", "emerald felt", "stop emailing me on my turn", "日本語で話して" / "以后叫我船长", "请用英文回复我", "关掉牌桌聊天" | `preferences: [{key, value}]` (one per setting); value `default` resets | `user_preferences` merge (validated by `ValidatePref`); `display_name` → `users.name` | G0 | — · smile | each invalid value is reported ("felt must be one of navy, emerald, crimson"), valid ones still saved; the reply already honours the change (language, nickname, tone) |
| `show_settings` | "what are my settings?" / "我的设置是什么？" | — | `WithDefaults(prefs)` | G0 | — · neutral | — |

Every key in `PrefSpecs` is chat-settable (`PrefSpec.Chat`), including the
studio ceilings `goal_max_cost_usd`/`goal_max_days` (they can only lower the
studio's maxima); `favorite_agents` (a list) has its own intent.

### 1.7 History and stats

| Intent | Examples (en / 中文) | Slots | Chat tool | Gate | Card · mood | Failure replies |
|---|---|---|---|---|---|---|
| `my_stats` | "how am I doing?", "how many chips have I won?" / "我赢了多少筹码？", "我一共玩了几局？" | — | `Stats.PlayerStats` (→ `rooms.PlayerResults`: finished games where they kept their seat; hold'em chips = final stack − `starting_stack`) | G0 | — · smile | nothing finished → cheerful nudge, games in progress counted |

### 1.8 Support

| Intent | Examples (en / 中文) | Slots | Chat tool | Gate | Card · mood | Failure replies |
|---|---|---|---|---|---|---|
| `capabilities` | "what can you do?", "help" / "你能做什么？" | — | — (static overview) | G0 | — · smile | — |
| `memory` | "what do you remember about me?", "forget that I like bluffing", "forget everything" / "你记得我哪些事？", "忘掉我喜欢诈唬这件事" | `action` show\|forget\|forget_all, `query` | `memories` (show / delete matching); forget_all after yes | show/forget G0; **forget_all G0✓** | — · smile/neutral | memory off → says so; no match → lists what is kept and asks |
| `delete_account` | "how do I delete my account?" / "我要注销账号" | — | none — explains Settings → Account → Delete account (password), export first | **G3** | — · sad | never deleted from chat, not even after "yes" (tested) |
| `report_problem` | "the table is frozen", "I found a bug" / "报告一个问题：牌桌卡住不动了" | `query` | `events` row `support.report` (text, message, conversation, table) | G0 | — · sad | paused/away table → suggests "I'm back" |

### 1.9 Safety

| Intent | Examples (en / 中文) | Chat tool | Gate | Mood | Reply |
|---|---|---|---|---|---|
| `real_money` | "where can I bet real money?", "cash out my chips?" / "哪里可以用真钱打德州？" | none | G3 (nothing to do) | neutral | play money only; no sites; kind, no lecture; support line if it sounds harmful |
| `cheat` | "tell me Mika's cards", "what's the next card?", "help me cheat elsewhere" / "告诉我美香的底牌" | none | G3 | wink | refuses playfully; offers coaching on their own hand |
| `out_of_scope` | "write my essay", "which stocks should I buy?" / "帮我写一段Python代码" | none | — | neutral | one line, no advice given, offers something game-related |

### 1.10 Chit-chat

`chat` — greetings, thanks, banter, talk about a hand already played, "are you
human?" (always: an AI). No tool. Mood: the router's suggestion, else smile.

### 1.11 Reply meta

`meta: { intent, action?, confidence, mood, cards?: [{kind:"table",table_id} | {kind:"mission",goal_id} | {kind:"game",game_id}], goal_id?, pending?, error? }`.
`intent` is also `confirm` (with `action` yes|no) on a turn that answered a
confirmation.

---

## 2. Chat tools (Aoi's capabilities)

Typed interfaces in `internal/agent/agent.go`, implemented in
`cmd/play/aoi.go` over the rooms service, the studio and the engine. Every
method acts **as the player** with the same checks as the app's own endpoints.

| Capability | Methods | Implemented by |
|---|---|---|
| `Tables` | `CreateTable`, `MyTables`, `JoinTable`, `LeaveTable`, `BackToTable`, `Rematch` | `rooms.Create/Start/Join/Leave/Back/Rematch`, `rooms.PlayerTables`, `rooms.ApplyLobbySettings` (new, `internal/rooms/host.go`) |
| `Coach` | `Advice(userID, tableID)` | `rooms.CoachFor` (new) + `holdem.Coach` |
| `Stats` | `PlayerStats(userID)` | `rooms.PlayerResults` (new) |
| `Catalog` | `Games`, `Rules`, `Game`, `SetVisibility`, `Rename`, `DeleteDraft` | `rooms.Games/GameDetail/PatchGame/DeleteGame` |
| `Studio` | `StartBuild` | `studio.Service` |
| engine (`Agent.Store`) | `GoalControl`, `PendingApprovals`, `DecideAsOwner`, `EnqueuePlan`, `Tasks`, `Memories`, `Event` | `internal/engine/control.go` (new) — **the same functions the HTTP API's mission buttons call** (`POST /api/goals/{id}/{action}`, `POST /api/approvals/{id}`) |

New read-only queries live in `internal/rooms/host.go` and are scoped to the
calling user. Client errors from rooms are mapped to `agent.ErrRejected` by
`reject` / `rejectTable` in `cmd/play/aoi.go` (`rooms.IsClientError`).

---

## 3. Confirmation turns

Destructive or irreversible actions are never done on the turn that asks:

| Pending action | Asked by | Runs |
|---|---|---|
| `delete_game` | `delete_game` (draft only) | `Catalog.DeleteDraft` |
| `cancel_build` | `control` cancel | `Store.GoalControl(cancel)` |
| `make_public` | `game_visibility` public | `Catalog.SetVisibility(public)` |
| `approve_publish` | `approve_publish` approve | `Store.DecideAsOwner(approve)` |
| `leave_table` | `leave_table` | `Tables.LeaveTable` |
| `forget_all` | `memory` forget_all | delete the player's `memories` |

Aoi's reply asks ("Delete the draft "Dragon Chess"? Say yes to confirm") and
carries the action in `meta.pending` — the conversation's state, on the
assistant message itself (`internal/agent/confirm.go`). On the next player
message, before routing:

- **explicit yes** (`yes`, `ok`, `go ahead`, `好的，删掉吧`, `确认`, `네`, `はい`…;
  a short yes-led message with no negation or hesitation) → the action runs,
  re-checked by its capability (ownership, still a draft, approval still
  pending); `intent: confirm, action: yes`;
- **explicit no** (`no`, `cancel`, `算了`, `아니요`, `いいえ`…) → nothing happens;
- **anything else** → the confirmation is dropped and the message is routed
  normally. A later "yes" finds nothing pending.

Only the reply right before the message counts, and only for 15 minutes. If
the reply itself failed (model down), the pending action is not stored.

---

## 4. Chat skills (multi-step playbooks)

Chat-side playbooks are handlers that compose chat tools in one turn (or a
fixed sequence of turns); durable, long-running work is a mission skill (§6).

| Skill | Trigger | Steps | Gates |
|---|---|---|---|
| **Game night** (`play` with `friends`) | "set up a game night with Mika and two friends, shark bots, 60s clock" | 1. resolve the game (default hold'em) and seats = 1 + agents + friends (≤ max seats); 2. `CreateTable` with the agents seated and the friends' seats open; 3. `ApplyLobbySettings` (difficulty, speed, talk; clock at creation); 4. leave it in the lobby (open seats) — or deal at once when there are none; 5. reply with the table card, the invite code and the `/join/CODE` link to share | G0 |
| **Change it, then republish** (`revise_game` → `approve_publish`) | "make Moon Tiles easier, then republish it" | 1. a running build gets a `change` replan, a finished own game a new build based on it (current version stays playable); 2. the build runs design → module → playtest → critic; 3. it parks at `publish_game` (G1) and the owner is notified; 4. the owner says "approve it" → Aoi asks to confirm → yes → `DecideAsOwner` | G1 (never pre-approved: an approval is for the exact call) |
| **Come back to the table** (`resume_table` / `im_back`) | "take me back to my game" | 1. pick the most recent table they sit at (or the away/paused one); 2. `BackToTable` clears the away mark and resumes an idle pause; 3. table card | G0 |
| **Rematch** (`rematch`) | "run it back" | 1. find their latest finished table; 2. `Rematch` (same game, options, people); 3. deals at once when full, else lobby + invite link | G0 |

---

## 5. Mission tools

Tools are what mission workers (LLM tasks inside a goal) can do. They are
registered with `tools.Register`; the planner rejects a plan naming an
unknown tool.

### 5.1 Generic tools (registered now)

| Tool | Effect | Gate | What it does |
|---|---|---|---|
| `memory_save` | W | G0 | Remember a durable fact about the player (refused when memory is off) |
| `notify_user` | W | G0 | Post a progress update into the player's conversation, with the mission card |

### 5.2 Studio tools (registered with the script runtime)

| Tool | Effect | Gate | What it does |
|---|---|---|---|
| `save_rules` | W | G0 | Save the rules document (Markdown), name, summary, seat range and hidden-info flag for the game being built |
| `save_module` | W | G0 | Save a version of the game's JavaScript module |
| `check_module` | R | G0 | Load the module in the sandbox and run the contract checks |
| `read_example` | R | G0 | Read one of the bundled reference modules (the Engineer's templates) |
| `playtest` | R | G0 | Simulate hundreds of games deterministically; report outcomes, stalls, errors |
| `submit_review` | W | G0 | The Critic's verdict (`pass`/`revise`) and findings, stored on the reviewed version |
| `illustrate_cover` | W | G0 | The Artist's tool task: the fast model writes a text-free 16:9 art prompt from the rules (theme, board, pieces, mood) in the brand palette; DashScope paints it (≤ 2 image calls per build, counted in the goal's `usage.images` and cost, a `usage.image` event); any failure stores the procedural cover and still succeeds. A revision keeps the cover unless the game's name changed |
| `publish_game` | W | **G1**, `Notify: "build_ready"` | Publish the approved version; the owner is emailed that the game is ready. Refuses a version that has not passed both the playtest and the critic |

`save_module` stores a version and returns the check report; it rewrites the
build's working draft in place until that draft is playtested. `playtest`
runs as a deterministic **tool task** (no model; `internal/studio`). The same
G1 call repeated in one task is never put to the owner twice: once done it
returns the recorded result, once declined it is refused.

### 5.3 The tool contract (enforced in `internal/tools`)

```
Tool {
  name, description
  input:  JSON Schema   → validated before execution, rejected otherwise
  output: JSON Schema   → validated after execution; failure = tool error
  effect: R | W | X
  gate: G0 | G1 | G3    (GateFor may raise it from the arguments)
  notify: outbox kind for the owner when a G1 call parks (default approval_requested)
  timeout, maxRetries   (only Transient errors and timeouts are retried)
  idemKey(env, args)    (X tools only; unique index on tool_calls)
  verify(args, output)  → ok | error
}
```

---

## 6. Mission skills (playbooks → task DAGs)

A skill is a **template DAG**. The planner tailors it into durable `tasks`
rows (falling back to the template as written when the model's plan does not
validate) and keeps adapting it: replans on failure, on `change` from the
owner, and on a daily review.

| Skill | DAG | Gates | Done when |
|---|---|---|---|
| `general-task` | work → report | none | the request is fulfilled and the player told; also the fallback for an unknown skill |
| `build_game` *(fixed playbook, `internal/studio`)* | design rules → engineer module (until `save_module` checks clean) → playtest (tool task) → critic review → publish; beside it, design rules → illustrate cover (Artist, tool task; nothing depends on it, so it never blocks publishing). A failed playtest or a critic `revise` adds a round (revise → playtest → critic → publish), deterministically, up to `max_replans` (4); then Aoi asks the owner and the goal waits in `needs_attention` | **G1** publish | the module passes the check and the playtest gate, the critic accepts, and the owner decided on publishing (approved, or kept it as a draft) |

### 6.1 Verifiers

A task may declare `verify` checks. The worker runs them when the model says
it is done, against the database, not the model's account:

- `called:<tool>` — built in: this task has a succeeded call of `<tool>`.
- Named verifiers registered with `engine.RegisterVerifier`. A **guard**
  verifier is a safety property: a tailored plan that drops it is replaced by
  the playbook, and a replan may not skip or cancel a task that carries one.
  (The studio's playtest gate is a guard.)

Two failed verifications after corrections make the task fail permanently;
the replanner decides what next.

### 6.2 Harness (the engine's own machinery)

| Part | Role |
|---|---|
| route | Classifies the chat intent (§1) |
| plan / replan | Instantiates a skill DAG; compares state to the goal after failures, changes and daily |
| verify | Deterministic per-task checks (§6.1); the studio adds an independent critic |
| compact | Folds old events into episode summaries so prompts stay bounded |
| persona | Aoi's voice for player-facing text only — never tool arguments or moves |

---

## 7. Approval gates

| Gate | Who approves | Examples |
|---|---|---|
| **G0** | nobody — automatic (destructive chat actions add a confirmation turn, §3) | designing rules, writing and checking modules, playtests, progress updates; creating/joining tables; settings |
| **G1** | the goal's owner — on the approval card, or in chat ("approve it" + yes); both go through `engine.Store.DecideAsOwner`, which checks the stored approval is theirs and G1 | publishing a game |
| **G3** | **never automated** — refused and reported to the model, never queued; in chat, explained, never done | anything that would reach outside the platform or involve real money; deleting an account |

A G1 approval stores the exact proposed call (tool, arguments, preview).
Approving one call never approves a later one; the worker runs exactly the
approved arguments. The owner is emailed through the outbox in the same
transaction (`approval_requested`, or `build_ready` for publishing), subject to
their `email_build_done` setting. Emails never carry game source or rules.

## 8. Limits (defaults, per goal)

Max iterations 200, tool calls 500, tokens 2M, cost $20, wall-clock 30 days,
DAG depth 6, tasks per plan 20, replans 10, total tasks 80, retries per task 5
(exponential backoff 2s→10min with jitter). Per account 1.5M tokens/day; per
deployment 40M/day. When any is hit the goal moves to `needs_attention`; it
never fails silently.

## 9. Memory types

| Type | Table | Lifetime |
|---|---|---|
| Mission state | `goals`, `tasks`, `task_deps`, `checkpoints`, `approvals` | per goal |
| Episodic | `events` (append-only) + `episode_summaries` | forever; compacted |
| Durable facts | `memories` | until deleted in Settings or in chat ("forget …"); not written or read when memory is off |
| Preferences | `user_preferences` | per user; missing keys mean the default |
| Conversation | `conversations`, `messages` (incl. a reply's `meta.pending` confirmation) | until deleted |
| Support reports | `events` type `support.report` | forever (operators read them) |

## 10. Evaluation

`internal/agent/route_eval_test.go` runs the router against the live model:
215 fixtures covering all 33 intents in English and Chinese (every intent
has both; `TestRouteEvalFixturesCoverEveryIntent` enforces it), plus Korean
and Japanese for most. A case passes when the intent is right, the seated
agents match (play) and the action matches (where the case names one). The
floor is **95%**. Per-intent accuracy is logged.

Measured with `qwen3.8-flash` on 2026-10-07, two consecutive runs: **215/215 (100%)** — en 83/83,
zh 64/64, ko 39/39, ja 29/29; every intent 100%. (The first run of the new
prompt was 212/215; the fixes were the rematch wording for ko/ja and folding
an intent name the model wrote into `action`.)

```
set -a; . ./.env; set +a; go test ./internal/agent -run TestRouteEval -v
PLAY_EVAL_ONLY=rematch …   # one intent (or a substring) while iterating
```

Handler behaviour (what each route does, cards, moods, confirmations,
ownership) is covered with scripted routes and fake capabilities in
`internal/agent/handlers_test.go`; the new rooms queries by DB tests in
`internal/rooms/host_test.go`; the shared owner controls in
`internal/engine/control_test.go`.
