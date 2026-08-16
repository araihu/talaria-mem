#!/bin/sh
set -eu

TASK=cp1-a-12
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../../../../.." && pwd)
TMP=$(mktemp -d "${TMPDIR:-/tmp}/talaria-mem-cp1-a-12-red.XXXXXX")
trap 'rm -rf "$TMP"' EXIT

SOURCE_COMMIT=94c9fd4565e0a2d6bdfab7736ff6496860ddf945
SOURCE_TREE=738a55f79972c464e71abebe9027e9b1762b8e39
test "$(git -C "$ROOT" rev-parse "$SOURCE_COMMIT^{tree}")" = "$SOURCE_TREE"
git archive --format=tar "$SOURCE_COMMIT" | tar -xf - -C "$TMP"

set +e
(
	cd "$TMP"
	go test ./internal/adapters/sqlite -run '^TestMigrationCP1A12' -count=1 -v
)
RC=$?
set -e
printf 'task=%s scope=T3-migration-clock-and-final-validation phase=RED expected_exit=1 actual_exit=%s\n' "$TASK" "$RC"
test "$RC" -eq 1
