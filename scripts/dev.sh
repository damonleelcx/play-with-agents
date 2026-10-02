#!/usr/bin/env bash
# Run the ACT backend locally against the dev Postgres, with settings from .env.
#   scripts/dev.sh            → http://localhost:8080 (API + built web app)
#   npm --prefix web run dev  → http://localhost:5173 (hot-reload UI, proxies /api)
# Without SMTP settings, verification/reset links are printed to this log.
# A new local Postgres uses trust auth, reachable only on 127.0.0.1 - no
# password lives in this repository. ACT_DATABASE_URL in .env points at it.
set -euo pipefail
cd "$(dirname "$0")/.."
[ -f .env ] || { echo "create .env first (see README → Run it locally)" >&2; exit 1; }
if ! docker ps --format '{{.Names}}' | grep -qx act-pg; then
  docker start act-pg >/dev/null 2>&1 || docker run -d --name act-pg -e POSTGRES_USER=act -e POSTGRES_HOST_AUTH_METHOD=trust \
    -e POSTGRES_DB=act -p 127.0.0.1:55850:5432 postgres:17 >/dev/null
fi
until docker exec act-pg pg_isready -U act >/dev/null 2>&1; do sleep 1; done
docker exec act-pg psql -U act -tAc "SELECT 1 FROM pg_database WHERE datname='act_dev'" | grep -q 1 \
  || docker exec act-pg psql -U act -c "CREATE DATABASE act_dev" >/dev/null
set -a; . ./.env; set +a
go build -o bin/act ./cmd/act
exec ./bin/act serve
