#!/bin/sh
set -eu

TASK=cp1-a-8
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../../../../.." && pwd)
TMP=$(mktemp -d "${TMPDIR:-/tmp}/talaria-mem-cp1-a-8-red.XXXXXX")
trap 'rm -rf "$TMP"' EXIT

SOURCE_COMMIT=34f54d2159a41e6abd022b66c22c18d9755165d1
SOURCE_TREE=a6f8ee7717694265d9eadf0df1e17acb4dba1f7f
test "$(git -C "$ROOT" rev-parse "$SOURCE_COMMIT^{tree}")" = "$SOURCE_TREE"
git -C "$ROOT" archive --format=tar "$SOURCE_COMMIT" | tar -xf - -C "$TMP"

OVERLAY=testdata/cp1-a-8/red-overlay/internal/adapters/sqlite/cp1_a8_migration_authority_red_test.go
OVERLAY_SHA=63a291af4fe762f256fbb098f7b32e72080ee54d10c8ccf5ff8b6c71a0fc1905
test "$(shasum -a 256 "$ROOT/$OVERLAY" | awk '{print $1}')" = "$OVERLAY_SHA"
mkdir -p "$TMP/internal/adapters/sqlite"
cp "$ROOT/$OVERLAY" "$TMP/internal/adapters/sqlite/"

set +e
(
	cd "$TMP"
	go test ./internal/adapters/sqlite -run '^TestMigrationAuthorityCP1A8' -count=1
)
RC=$?
set -e
printf 'task=%s scope=T3-migration-authority phase=RED expected_exit=1 actual_exit=%s\n' "$TASK" "$RC"
test "$RC" -eq 1
