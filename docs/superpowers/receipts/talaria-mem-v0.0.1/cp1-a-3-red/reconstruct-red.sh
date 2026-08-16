#!/bin/sh
set -eu

SOURCE_COMMIT=1ab97d1c1429ba188c3dd614db015c2d729c9d51
SOURCE_TREE=179bef1465f9c3a7ecb2088ff7181481d8389e4f
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../../../../.." && pwd)
TMP=$(mktemp -d "${TMPDIR:-/tmp}/talaria-mem-cp1-a-3-red.XXXXXX")
trap 'rm -rf "$TMP"' EXIT

test "$(git -C "$ROOT" rev-parse "$SOURCE_COMMIT^{tree}")" = "$SOURCE_TREE"
git -C "$ROOT" archive --format=tar "$SOURCE_COMMIT" | tar -xf - -C "$TMP"

copy_overlay() {
	source=$1
	target=$2
	test "$(shasum -a 256 "$ROOT/$source" | awk '{print $1}')" = "$3"
	mkdir -p "$(dirname -- "$TMP/$target")"
	cp "$ROOT/$source" "$TMP/$target"
}

copy_overlay testdata/cp1-a-3/red-overlay/cmd/talaria-mem/cp1_a3_bootstrap_red_test.go \
	cmd/talaria-mem/cp1_a3_bootstrap_red_test.go \
	517228f6dafdd5b754a85ead14d7fda39f9b95d27dba7577ed1da67793657e44
copy_overlay testdata/cp1-a-3/red-overlay/internal/adapters/sqlite/cp1_a3_migration_red_test.go \
	internal/adapters/sqlite/cp1_a3_migration_red_test.go \
	ee885e836aa2565c2af61ae900dfa128ae83d0d40e4bf04c7627949b7356d96c
copy_overlay testdata/cp1-a-3/red-overlay/internal/ports/cp1_a3_activation_red_test.go \
	internal/ports/cp1_a3_activation_red_test.go \
	11190e9b8d35c0b0fe75b27011055c72bd8eef52786c448e3d21d609953f4b61
copy_overlay testdata/cp1-a-3/red-overlay/internal/adapters/sqlite/cp1_a3_schema_red_test.go \
	internal/adapters/sqlite/cp1_a3_schema_red_test.go \
	1aa72ed3fa9bd3ec02eaca37b16705028e958900b708fa82bbb45feea93980a5
copy_overlay testdata/bootstrap/cp1-a-3-missing-output-writer.json \
	testdata/bootstrap/cp1-a-3-missing-output-writer.json \
	fe74355a2604acbb90f18f537bb852792ae81914b5c0fdaeba1887e846096169
copy_overlay testdata/sqlite/cp1-a-3-migration-ordering.json \
	testdata/sqlite/cp1-a-3-migration-ordering.json \
	497443e98c068c9e561642e29caaf59d5e0904cc7a2cb9701940d9200b14f303
copy_overlay testdata/domain/cp1-a-3-activation-epoch.json \
	testdata/domain/cp1-a-3-activation-epoch.json \
	c269f7648427c3694097d0ba411d0952ed8249f0ffe726305a77155954dfff30
copy_overlay testdata/sqlite/cp1-a-3-schema-failure-stage.json \
	testdata/sqlite/cp1-a-3-schema-failure-stage.json \
	943a5fbeb44250410844ad6fd7a0083131e9accdda556a5ee8f2cb0f015a3615

run_red() {
	label=$1
	shift
	output="$TMP/$label.out"
	set +e
	(
		cd "$TMP"
		"$@"
	) >"$output" 2>&1
	status=$?
	set -e
	test "$status" -eq 1
	printf 'task=cp1-a-3 scope=%s phase=RED tested_source_commit=%s tested_source_tree=%s expected_exit=1 actual_exit=%s\n' "$label" "$SOURCE_COMMIT" "$SOURCE_TREE" "$status"
	cat "$output"
}

run_red t1-bootstrap go test ./cmd/talaria-mem ./internal/testutil -count=1
run_red t2-domain go test ./internal/domain ./internal/ports -run 'TestResolutionState|TestLimits|TestKeyDeriver|TestNormalizeV1|TestManagedFileStore|TestActivationJournal|TestFTSConfig|TestSQLiteFull' -count=1
run_red t3-schema go test ./internal/adapters/sqlite -run 'TestSchema|TestTransaction|TestFTS|TestFTSTokenizer|TestUnicodeDiacritic|TestSQLiteFull|TestActivationJournal' -count=1
run_red t3-migration go test ./internal/adapters/sqlite -run 'TestMigrationRoundTrip' -count=1
