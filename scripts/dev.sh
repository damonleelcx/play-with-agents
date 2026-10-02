#!/usr/bin/env bash
# Run the Play with Agents backend locally against the dev Postgres, with
# settings from .env.
#   scripts/dev.sh            → http://localhost:8090 (API + built web app)
#   npm --prefix web run dev  → http://localhost:5173 (hot-reload UI, proxies /api to :8090)
# Without SMTP settings, verification/reset links are printed to this log.
#
# The database is the container play-pg on 127.0.0.1:55860 with trust auth,
# reachable only from this machine — no password lives in this repository.
# Two databases: `play_dev` for manual runs (PLAY_DATABASE_URL in .env) and
# `play` for the tests (their default; see internal/engine/harness_test.go).
set -euo pipefail
cd "$(dirname "$0")/.."
[ -f .env ] || { echo "create .env first (see README → Run it locally)" >&2; exit 1; }

if ! docker ps --format '{{.Names}}' | grep -qx play-pg; then
  docker start play-pg >/dev/null 2>&1 || docker run -d --name play-pg -e POSTGRES_USER=play -e POSTGRES_HOST_AUTH_METHOD=trust \
    -e POSTGRES_DB=play -p 127.0.0.1:55860:5432 postgres:17 >/dev/null
fi
# Bounded: a container that is not ready in 60 s is broken, not slow.
ready=0
for _ in $(seq 1 60); do
  if docker exec play-pg pg_isready -U play >/dev/null 2>&1; then ready=1; break; fi
  sleep 1
done
[ "$ready" = 1 ] || { echo "play-pg did not become ready in 60s (docker logs play-pg)" >&2; exit 1; }
for db in play play_dev; do
  docker exec play-pg psql -U play -d postgres -tAc "SELECT 1 FROM pg_database WHERE datname='$db'" | grep -q 1 \
    || docker exec play-pg psql -U play -d postgres -c "CREATE DATABASE $db" >/dev/null
done

set -a; . ./.env; set +a
export PLAY_ADDR="${PLAY_ADDR:-:8090}"
export PLAY_DATABASE_URL="${PLAY_DATABASE_URL:-postgres://play@127.0.0.1:55860/play_dev?sslmode=disable}"
go build -o bin/play ./cmd/play
exec ./bin/play serve
