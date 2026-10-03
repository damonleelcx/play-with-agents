# Development

The technical side of Play with Agents: how it is built, how to run it, test
it, configure it and deploy it. For what the product is, see the
[README](../README.md).

## How it is built

One Go binary, `play`, and one Postgres database.

```
cmd/play            serve | web | worker | migrate | mailcheck
internal/agent      Aoi's chat: router (intents), capabilities (Tables, Studio, Catalog), settings schema
internal/persona    Aoi's soul (system prompts), moods → faces
internal/engine     durable missions: goals, task DAGs, leased queue with fencing, planner/replanner,
                    checkpoints, G1 approvals, budgets, verifiers, scheduler, timeline
internal/tools      tool contract and registry (schemas, gates, idempotency, verification)
internal/skills     playbooks (template DAGs)
internal/games      the game contract; holdem (built in) and script (JavaScript games, sandboxed)
internal/rooms      tables: seats, moves, turn clocks, agent players, table chat, SSE
internal/notify     email outbox (enqueued in the same transaction as the fact it announces)
internal/tts        Aoi's voice: Fish Audio MP3, text cleaner, LRU cache
internal/auth       accounts, argon2id passwords, sessions, verify/reset emails
internal/httpapi    JSON API under /api, cookie session play_session, streamed chat
web/                the React app, embedded into the binary at build time
```

The engine's guarantees, in one paragraph: work is claimed with `FOR UPDATE
SKIP LOCKED` and a lease whose epoch is a fencing token, so a worker that lost
its lease cannot write anything; every step is checkpointed, so a crash loses
at most the step in flight; side effects carry idempotency keys; a G1 action
parks its task with the exact proposed arguments until the owner decides, and
only those arguments run; budgets stop a goal and ask for a person rather than
running on.

## Run it locally

Needs Go 1.26, Node 20 and Docker.

1. Create `.env` in the repo root (git-ignored; never commit it):

   ```sh
   PLAY_LLM_API_KEY=…        # OpenAI-compatible key for the model endpoint
   PLAY_DATABASE_URL=postgres://play@127.0.0.1:55860/play_dev?sslmode=disable
   PLAY_ADDR=:8090
   PLAY_PUBLIC_ORIGIN=http://localhost:8090
   PLAY_COOKIE_SECURE=false
   ```

2. `scripts/dev.sh` — starts (or creates) the `play-pg` container on
   `127.0.0.1:55860` (user `play`, trust auth, local only), makes the `play`
   and `play_dev` databases, builds `bin/play` and runs `play serve` on
   http://localhost:8090. Without SMTP settings, verification and reset links
   are printed to the log.
3. `npm --prefix web install && npm --prefix web run dev` — the hot-reload UI
   on http://localhost:5173, proxying `/api` to :8090.

## Tests

```sh
go vet ./...
go test ./...
```

Database tests run against the local `play-pg` (database `play`; the agent
tests create `play_agent_test` next to it) or `PLAY_TEST_DATABASE_URL`, and skip
when no database is reachable. The router's live evaluation runs only when a
key is present:

```sh
set -a; . ./.env; set +a; go test ./internal/agent -run TestRouteEval -v
```

## Configuration

Every knob is an environment variable (`internal/config`); the production
values are in `deploy/k8s/20-config.yaml`, secrets in AWS Secrets Manager
(`play/prod`) synced by External Secrets.

| Variable | Default | |
|---|---|---|
| `PLAY_DATABASE_URL` | — (required) | Postgres DSN |
| `PLAY_ADDR` | `:8080` | listen address |
| `PLAY_PUBLIC_ORIGIN` | — | origin every emailed link is built on; mail is off without it |
| `PLAY_COOKIE_SECURE` | `true` | |
| `PLAY_SESSION_TTL` | `720h` | |
| `PLAY_LLM_BASE_URL`, `PLAY_LLM_API_KEY` | token-plan endpoint, — | model endpoint |
| `PLAY_LLM_MODEL`, `PLAY_LLM_FAST_MODEL` | `qwen3.8-max`, `qwen3.8-flash` | replies/planning; routing/summaries |
| `PLAY_SMTP_HOST`, `_PORT`, `_USERNAME`, `_PASSWORD`, `_FROM`, `_REPLY_TO` | —, 587 | mail relay (STARTTLS, verified) |
| `PLAY_ADMIN_EMAILS` | — | operators, comma separated (verified addresses only) |
| `PLAY_WORKERS`, `PLAY_LEASE` | 2, `90s` | mission workers |
| `PLAY_TABLE_WORKERS`, `PLAY_TABLE_LEASE` | 4, `15s` | table workers |
| `PLAY_ACCOUNT_DAILY_TOKENS`, `PLAY_DEPLOYMENT_DAILY_TOKENS` | 1.5M, 40M | spending ceilings per UTC day |
| `PLAY_TTS_API_KEY`, `PLAY_TTS_VOICE_ID`, `PLAY_TTS_MODEL` | —, —, `s2.1-pro-free` | Aoi's voice (Fish Audio); no key or voice id = no voice |

## Deploy

Namespace `play` on the heros k3s node, image in ECR repository `play`,
database `play` on the shared Postgres.

```sh
INSTANCE=i-… deploy/release.sh --dry-run   # build linux/arm64, push, server-side dry run
INSTANCE=i-… deploy/release.sh             # … and apply, pinned by digest
```

`deploy/deploy.sh` is idempotent: it creates the database role and database
(reading the DSN on the node, so no password travels in the SSM payload),
admits `play` pods to the shared Postgres and mail relay by namespace and
label, adds `play` to the nightly backup, applies the manifests and waits for
`play-web` and `play-worker`.
