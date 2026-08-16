#!/bin/sh
set -eu

TASK=cp1-a-7
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../../../../.." && pwd)
TMP=$(mktemp -d "${TMPDIR:-/tmp}/talaria-mem-cp1-a-7-red.XXXXXX")
trap 'rm -rf "$TMP"' EXIT

SOURCE_COMMIT=e592d016577e4d9433ec1f624a11cfbfa3dc66d1
SOURCE_TREE=f73fd6c742c2e8f70ed05a13e97d06daf55b5553
test "$(git -C "$ROOT" rev-parse "$SOURCE_COMMIT^{tree}")" = "$SOURCE_TREE"
git -C "$ROOT" archive --format=tar "$SOURCE_COMMIT" | tar -xf - -C "$TMP"

OVERLAY=testdata/cp1-a-7/red-overlay/internal/adapters/sqlite/cp1_a7_migration_authority_red_test.go
OVERLAY_SHA=416000e17c7c5dabea3ca0450e67da3970ddadcedeee347dcb6bfb62ab0ddc6a
test "$(shasum -a 256 "$ROOT/$OVERLAY" | awk '{print $1}')" = "$OVERLAY_SHA"
mkdir -p "$TMP/internal/adapters/sqlite"
cp "$ROOT/$OVERLAY" "$TMP/internal/adapters/sqlite/"

set +e
(
	cd "$TMP"
	go test ./internal/adapters/sqlite -run '^TestMigrationAuthorityCP1A7' -count=1
)
RC=$?
set -e
printf 'task=%s scope=T3-migration-authority phase=RED expected_exit=1 actual_exit=%s\n' "$TASK" "$RC"
test "$RC" -eq 1
