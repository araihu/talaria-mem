#!/bin/sh
set -eu

TASK=cp1-a-11
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../../../../.." && pwd)
TMP=$(mktemp -d "${TMPDIR:-/tmp}/talaria-mem-cp1-a-11-red.XXXXXX")
trap 'rm -rf "$TMP"' EXIT

SOURCE_COMMIT=733e11773c08587774c8ce033d060f6f4281f033
SOURCE_TREE=11198d494697b3e1ab9ce81a3be99acd41d53c85
test "$(git -C "$ROOT" rev-parse "$SOURCE_COMMIT^{tree}")" = "$SOURCE_TREE"
git archive --format=tar "$SOURCE_COMMIT" | tar -xf - -C "$TMP"

set +e
(
	cd "$TMP"
	go test ./internal/adapters/sqlite -run '^TestMigrationCP1A11' -count=1 -v
)
RC=$?
set -e
printf 'task=%s scope=T3-migration-authority-integrity phase=RED expected_exit=1 actual_exit=%s\n' "$TASK" "$RC"
test "$RC" -eq 1
