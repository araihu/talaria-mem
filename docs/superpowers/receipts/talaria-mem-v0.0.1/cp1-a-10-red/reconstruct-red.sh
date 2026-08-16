#!/bin/sh
set -eu

TASK=cp1-a-10
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../../../../.." && pwd)
TMP=$(mktemp -d "${TMPDIR:-/tmp}/talaria-mem-cp1-a-10-red.XXXXXX")
trap 'rm -rf "$TMP"' EXIT

SOURCE_COMMIT=acf81d078240a74158bea0efa73efad9141d5e60
SOURCE_TREE=10bc94edc43471eb4baf14fb9bb9ea901d03a7ab
test "$(git -C "$ROOT" rev-parse "$SOURCE_COMMIT^{tree}")" = "$SOURCE_TREE"
git archive --format=tar "$SOURCE_COMMIT" | tar -xf - -C "$TMP"

set +e
(
	cd "$TMP"
	go test ./internal/adapters/sqlite -run '^TestMigrationCP1A10RejectsMalformedJournalBeforeDirtyRecovery$' -count=1 -v
)
RC=$?
set -e
printf 'task=%s scope=T3-migration-journal-authority phase=RED expected_exit=1 actual_exit=%s\n' "$TASK" "$RC"
test "$RC" -eq 1
