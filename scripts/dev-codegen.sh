#!/usr/bin/env bash
# Regenerates sqlc + gqlgen code, but only when their inputs changed since the
# last run. Used by air (`make dev`) so plain Go edits don't pay for codegen.
set -euo pipefail
cd "$(dirname "$0")/.."

stamp=tmp/.codegen.sha
mkdir -p tmp

hash_cmd() { if command -v sha256sum >/dev/null; then sha256sum; else shasum -a 256; fi; }
current="$(cat sqlc.yaml gqlgen.yml internal/db/migrations/*.sql internal/db/queries/*.sql \
  internal/graph/schema/*.graphqls go.mod | hash_cmd | cut -d' ' -f1)"

if [[ -f "$stamp" && "$(cat "$stamp")" == "$current" ]]; then
  exit 0
fi

echo "codegen: inputs changed, running sqlc + gqlgen..."
go tool sqlc generate
go tool gqlgen generate
echo "$current" > "$stamp"
