#!/usr/bin/env bash
# Runs unit + e2e tests with cross-package coverage, strips generated code,
# prints the per-function report and fails if total coverage < THRESHOLD.
set -euo pipefail

THRESHOLD="${THRESHOLD:-90}"
OUT_DIR="${OUT_DIR:-coverage}"
mkdir -p "$OUT_DIR"

go test -tags e2e -count=1 -covermode=atomic \
  -coverpkg=./internal/... \
  -coverprofile="$OUT_DIR/raw.out" ./...

# Generated code is excluded from the metric (it is exercised, but not ours to cover).
grep -vE '/internal/db/sqlc/|/internal/graph/generated/|/internal/graph/model/models_gen\.go' \
  "$OUT_DIR/raw.out" > "$OUT_DIR/cover.out"

go tool cover -func="$OUT_DIR/cover.out" | tee "$OUT_DIR/func.txt"
go tool cover -html="$OUT_DIR/cover.out" -o "$OUT_DIR/cover.html"

total="$(awk '/^total:/ {sub("%", "", $3); print $3}' "$OUT_DIR/func.txt")"
echo "Total coverage (hand-written code): ${total}% (threshold ${THRESHOLD}%)"
awk -v t="$total" -v th="$THRESHOLD" 'BEGIN { exit (t + 0 < th + 0) ? 1 : 0 }' || {
  echo "FAIL: coverage below ${THRESHOLD}%" >&2
  exit 1
}
