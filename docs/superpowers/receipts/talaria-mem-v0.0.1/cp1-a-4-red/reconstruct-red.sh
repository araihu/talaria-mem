#!/bin/sh
set -eu

TASK=cp1-a-4
SOURCE_COMMIT=069281c7ec57b3b4ba51e3a51f4059dfa98b4925
SOURCE_TREE=2a7015f474098dde3ea0b393a365f52b3b0085e1
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../../../../.." && pwd)
TMP=$(mktemp -d "${TMPDIR:-/tmp}/talaria-mem-cp1-a-4-red.XXXXXX")
trap 'rm -rf "$TMP"' EXIT

test "$(git -C "$ROOT" rev-parse "$SOURCE_COMMIT^{tree}")" = "$SOURCE_TREE"
git -C "$ROOT" archive --format=tar "$SOURCE_COMMIT" | tar -xf - -C "$TMP"

copy_overlay() {
	source=$1
	target=$2
	want=$3
	test "$(shasum -a 256 "$ROOT/$source" | awk '{print $1}')" = "$want"
	mkdir -p "$(dirname -- "$TMP/$target")"
	cp "$ROOT/$source" "$TMP/$target"
}

copy_overlay testdata/cp1-a-4/red-overlay/cmd/talaria-mem/cp1_a4_bootstrap_red_test.go \
	cmd/talaria-mem/cp1_a4_bootstrap_red_test.go \
	eea657ba1c9e95dab8c9f99332a09b754d5e9d87077ddac6b7827865f139ee3f
copy_overlay testdata/cp1-a-4/red-overlay/internal/ports/cp1_a4_activation_red_test.go \
	internal/ports/cp1_a4_activation_red_test.go \
	9c95d369bc4f802d61a7704ba17e3644ff0f851b6aef70ed371623e7275d8707
copy_overlay testdata/cp1-a-4/red-overlay/internal/adapters/sqlite/cp1_a4_schema_red_test.go \
	internal/adapters/sqlite/cp1_a4_schema_red_test.go \
	6c47fe001cab06eb6c92d0dc1281a556fb3ac32d8789f37d64a5686945672293
copy_overlay testdata/cp1-a-4/red-overlay/internal/adapters/sqlite/cp1_a4_migration_red_test.go \
	internal/adapters/sqlite/cp1_a4_migration_red_test.go \
	fc0ca7bdcddb256539c540c86fd74875e355a00b1b2fa3f6360d58cfcbcab061
copy_overlay testdata/cp1-a-4/red-overlay/internal/scanner/cp1_a4_metadata_red_test.go \
	internal/scanner/cp1_a4_metadata_red_test.go \
	f4e904007efc565fca2afb51aa2442403e0101d49ffeb9f118bbe2b0d6d4f349
copy_overlay testdata/secrets/cp1-a-4-metadata-canary.json \
	testdata/secrets/cp1-a-4-metadata-canary.json \
	f939c7ff2467085cb326b21303ba083411abf0dd1659615992117e3f4cedd805
copy_overlay testdata/bootstrap/cp1-a-4-composition-registration.json \
	testdata/bootstrap/cp1-a-4-composition-registration.json \
	6f6e9e42b7253bf1180df08c305f81dad38b42caf968bc12935babe88b928587

run_red() {
	scope=$1
	shift
	set +e
	(
		cd "$TMP"
		"$@"
	)
	status=$?
	set -e
	test "$status" -eq 1
	printf 'task=%s scope=%s phase=RED tested_source_commit=%s tested_source_tree=%s expected_exit=1 actual_exit=%s\n' "$TASK" "$scope" "$SOURCE_COMMIT" "$SOURCE_TREE" "$status"
}

run_red T1-bootstrap go test ./cmd/talaria-mem ./internal/testutil -count=1
run_red T2-domain sh -c "go test ./internal/domain ./internal/ports -run 'TestResolutionState|TestLimits|TestKeyDeriver|TestNormalizeV1|TestManagedFileStore|TestActivationJournal|TestFTSConfig|TestSQLiteFull' -count=1"
run_red T3-sqlite-schema sh -c "go test ./internal/adapters/sqlite -run 'TestSchema|TestTransaction|TestFTS|TestFTSTokenizer|TestUnicodeDiacritic|TestSQLiteFull|TestActivationJournal' -count=1"
run_red T3-migration go test ./internal/adapters/sqlite -run 'TestMigrationRoundTrip' -count=1
run_red T4-scanner env TALARIA_SCANNER_NETWORK=disabled go test ./internal/scanner -run 'TestBoundary|TestRuleUpgrade|TestNoEgress' -count=1
