#!/usr/bin/env bash
# One-command dev bootstrap: env file, Postgres, migrations, live-reloading server.
set -euo pipefail
cd "$(dirname "$0")/.."

if [[ ! -f .env ]]; then
  cp .env.example .env
  echo "dev: created .env from .env.example"
fi

if ! docker info >/dev/null 2>&1; then
  echo "dev: docker is not running — start Docker/OrbStack and retry" >&2
  exit 1
fi

echo "dev: starting Postgres..."
docker compose -f deploy/docker-compose.yml up -d --wait

addr="$(grep -E '^HTTP_ADDR=' .env | cut -d= -f2 || true)"
port="${addr##*:}"; port="${port:-8080}"
cat <<MSG

  MediaBrain dev server (live reload)
    GraphQL playground   http://localhost:${port}/
    Debug / dev data     http://localhost:${port}/debug/
    Seed from CLI        make seed s=dummies args="count=100"

MSG

exec go tool air -c .air.toml
