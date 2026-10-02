# 01 · Intents, tools and skills

This document lists everything Aoi can be asked to do (**intents**), every
action an agent can take through the mission engine (**tools**), and the
multi-step playbooks it runs (**skills**). The router, the planner, the worker,
the approval gates and the UI are built from it. A capability that is missing
here does not exist in the product.

---

## 0. The boundary everything else is built around

Agents on this platform play games, explain them and build them. They never
move money (there is none: chips are play money), never reach outside the
platform, and never act for a player without asking when the action is
visible to other people. That is enforced by **gates** (§4) and by the game
contract (agents choose moves from what their seat can see), not by prompt
wording.

---

## 1. Intents

The router (`internal/agent`, fast model, JSON output) classifies every chat
turn into exactly one intent. With `confidence < 0.5` and a clarifying
question, Aoi asks instead of acting — an unsure router never creates a table
or starts a build.

| Intent | Example (en / 中文) | What happens before Aoi replies | Card | Mood |
|---|---|---|---|---|
| `play` | "deal me into hold'em with Mika and Ren" / "开一桌德州，叫上美香和蓮" | `Tables.CreateTable` with the game, roster agents and seat count; refused (nothing created) if the game seats fewer | `table` | wink |
| `build_game` | "Let's make a game where…" / "我们来做一个游戏吧…" | `Studio.StartBuild` starts a build mission (goal skill `build_game`) | `mission` | smile |
| `revise_game` | "Change the build to 3 players" / "把正在做的游戏改成三个人玩" | a running build gets a `change` replan; an owned, finished game gets a new build based on it | `mission` | smile |
| `rules` | "How do side pots work?" / "同花和顺子哪个大？" | the game's rules text (`Catalog.Rules`) is given to the reply as the authority | `game` (if a specific game) | neutral |
| `control` | "Pause the build" / "先暂停一下游戏制作" | pause / resume / cancel / status on the build | `mission` | neutral · smile · sad |
| `preference` | "Call me Captain" / "以后叫我船长" | the setting is validated against the Settings schema and saved; this reply already honours it | — | smile |
| `chat` | greetings, banter, "are you human?", real-money questions | nothing; real-money gambling is steered back to play money by the soul | — | router's suggestion, else smile |

Capabilities are interfaces in `internal/agent` (`Tables`, `Studio`, `Catalog`)
and every one may be nil: Aoi then says that part of the platform is not open
yet (mood `sad`) instead of failing the turn. A capability error wrapping
`agent.ErrRejected` is about the request and its message reaches the reply;
any other error is logged and the player hears a short apology.

Aoi's message `meta`: `{ intent, confidence, mood, cards?: [{kind:"table",table_id} | {kind:"mission",goal_id} | {kind:"game",game_id}], goal_id?, error? }`.

Measured routing accuracy (`internal/agent/route_eval_test.go`, live model,
30 fixtures en+zh, intent and seated agents both checked): **30/30** with
`qwen3.8-flash` on 2026-10-02. The floor is 90%.

---

## 2. Tools

Tools are what mission workers (LLM tasks inside a goal) can do. They are
registered with `tools.Register`; the planner rejects a plan naming an
unknown tool.

### 2.1 Generic tools (registered now)

| Tool | Effect | Gate | What it does |
|---|---|---|---|
| `memory_save` | W | G0 | Remember a durable fact about the player (refused when memory is off) |
| `notify_user` | W | G0 | Post a progress update into the player's conversation, with the mission card |

### 2.2 Studio tools (registered with the script runtime)

| Tool | Effect | Gate | What it does |
|---|---|---|---|
| `save_rules` | W | G0 | Save the rules document (Markdown), name, summary, seat range and hidden-info flag for the game being built |
| `save_module` | W | G0 | Save a version of the game's JavaScript module |
| `check_module` | R | G0 | Load the module in the sandbox and run the contract checks |
| `read_example` | R | G0 | Read one of the bundled reference modules (the Engineer's templates) |
| `playtest` | R | G0 | Simulate hundreds of games deterministically; report outcomes, stalls, errors |
| `submit_review` | W | G0 | The Critic's verdict (`pass`/`revise`) and findings, stored on the reviewed version |
| `publish_game` | W | **G1**, `Notify: "build_ready"` | Publish the approved version; the owner is emailed that the game is ready. Refuses a version that has not passed both the playtest and the critic |

`save_module` stores a version and returns the check report; it rewrites the
build's working draft in place until that draft is playtested. `playtest`
runs as a deterministic **tool task** (no model; `internal/studio`). The same
G1 call repeated in one task is never put to the owner twice: once done it
returns the recorded result, once declined it is refused.

### 2.3 The tool contract (enforced in `internal/tools`)

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

## 3. Skills (playbooks → task DAGs)

A skill is a **template DAG**. The planner tailors it into durable `tasks`
rows (falling back to the template as written when the model's plan does not
validate) and keeps adapting it: replans on failure, on `change` from the
owner, and on a daily review.

| Skill | DAG | Gates | Done when |
|---|---|---|---|
| `general-task` | work → report | none | the request is fulfilled and the player told; also the fallback for an unknown skill |
| `build_game` *(fixed playbook, `internal/studio`)* | design rules → engineer module (until `save_module` checks clean) → playtest (tool task) → critic review → publish. A failed playtest or a critic `revise` adds a round (revise → playtest → critic → publish), deterministically, up to `max_replans` (4); then Aoi asks the owner and the goal waits in `needs_attention` | **G1** publish | the module passes the check and the playtest gate, the critic accepts, and the owner decided on publishing (approved, or kept it as a draft) |

### 3.1 Verifiers

A task may declare `verify` checks. The worker runs them when the model says
it is done, against the database, not the model's account:

- `called:<tool>` — built in: this task has a succeeded call of `<tool>`.
- Named verifiers registered with `engine.RegisterVerifier`. A **guard**
  verifier is a safety property: a tailored plan that drops it is replaced by
  the playbook, and a replan may not skip or cancel a task that carries one.
  (The studio's playtest gate is a guard.)

Two failed verifications after corrections make the task fail permanently;
the replanner decides what next.

### 3.2 Harness (the engine's own machinery)

| Part | Role |
|---|---|
| route | Classifies the chat intent (§1) |
| plan / replan | Instantiates a skill DAG; compares state to the goal after failures, changes and daily |
| verify | Deterministic per-task checks (§3.1); the studio adds an independent critic |
| compact | Folds old events into episode summaries so prompts stay bounded |
| persona | Aoi's voice for player-facing text only — never tool arguments or moves |

---

## 4. Approval gates

| Gate | Who approves | Examples |
|---|---|---|
| **G0** | nobody — automatic | designing rules, writing and checking modules, playtests, progress updates |
| **G1** | the goal's owner, on the approval card | publishing a game |
| **G3** | **never automated** — refused and reported to the model, never queued | anything that would reach outside the platform or involve real money |

A G1 approval stores the exact proposed call (tool, arguments, preview).
Approving one call never approves a later one; the worker runs exactly the
approved arguments. The owner is emailed through the outbox in the same
transaction (`approval_requested`, or `build_ready` for publishing), subject to
their `email_build_done` setting. Emails never carry game source or rules.

## 5. Limits (defaults, per goal)

Max iterations 200, tool calls 500, tokens 2M, cost $20, wall-clock 30 days,
DAG depth 6, tasks per plan 20, replans 10, total tasks 80, retries per task 5
(exponential backoff 2s→10min with jitter). Per account 1.5M tokens/day; per
deployment 40M/day. When any is hit the goal moves to `needs_attention`; it
never fails silently.

## 6. Memory types

| Type | Table | Lifetime |
|---|---|---|
| Mission state | `goals`, `tasks`, `task_deps`, `checkpoints`, `approvals` | per goal |
| Episodic | `events` (append-only) + `episode_summaries` | forever; compacted |
| Durable facts | `memories` | until deleted in Settings; not written or read when memory is off |
| Preferences | `user_preferences` | per user; missing keys mean the default |
| Conversation | `conversations`, `messages` | until deleted |
