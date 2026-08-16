#!/bin/sh
set -eu

TASK=cp1-a-6
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../../../../.." && pwd)
TMP=$(mktemp -d "${TMPDIR:-/tmp}/talaria-mem-cp1-a-6-red.XXXXXX")
trap 'rm -rf "$TMP"' EXIT

SOURCE_COMMIT=70b667b083d268a12f797247d34ef1c1943c97a9
SOURCE_TREE=738fbd7de2bed2f73368fd054fbf96f2ba0bcbdb
test "$(git -C "$ROOT" rev-parse "$SOURCE_COMMIT^{tree}")" = "$SOURCE_TREE"
git -C "$ROOT" archive --format=tar "$SOURCE_COMMIT" | tar -xf - -C "$TMP"

OVERLAY=testdata/cp1-a-6/red-overlay/internal/adapters/sqlite/cp1_a6_migration_authority_red_test.go
OVERLAY_SHA=a71ff18710863dff062721fd167a5a12c533e10d93a02851c111653bb53cf349
test "$(shasum -a 256 "$ROOT/$OVERLAY" | awk '{print $1}')" = "$OVERLAY_SHA"
mkdir -p "$TMP/internal/adapters/sqlite"
cp "$ROOT/$OVERLAY" "$TMP/internal/adapters/sqlite/"

set +e
(
	cd "$TMP"
	go test ./internal/adapters/sqlite -run '^TestMigrationAuthorityCP1A6' -count=1
)
RC=$?
set -e
printf 'task=%s scope=T3-migration-authority phase=RED expected_exit=1 actual_exit=%s\n' "$TASK" "$RC"
test "$RC" -eq 1
