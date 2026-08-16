#!/bin/sh
set -eu

TASK=cp1-a-9
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../../../../.." && pwd)
TMP=$(mktemp -d "${TMPDIR:-/tmp}/talaria-mem-cp1-a-9-red.XXXXXX")
trap 'rm -rf "$TMP"' EXIT

SOURCE_COMMIT=dc491fd7060cde7e137ccca4931ee5b12db0928a
SOURCE_TREE=e978d791701520a16f6850c20f893e4bc7edf8ca
test "$(git -C "$ROOT" rev-parse "$SOURCE_COMMIT^{tree}")" = "$SOURCE_TREE"
git archive --format=tar "$SOURCE_COMMIT" | tar -xf - -C "$TMP"

set +e
(
	cd "$TMP"
	go test ./internal/adapters/sqlite -run '^TestMigrationDirtyRecoveryAfterSetVersionBeforeRun$' -count=1 -v
)
RC=$?
set -e
printf 'task=%s scope=T3-migration-crash-before-run phase=RED expected_exit=1 actual_exit=%s\n' "$TASK" "$RC"
test "$RC" -eq 1
